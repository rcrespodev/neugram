package server

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rcrespodev/neugram/internal/store"
)

func (s *Server) routesFlow() {
	s.routesInstructions()
	s.mux.HandleFunc("GET /tasks", s.listTasks)
	s.mux.HandleFunc("GET /tasks/{key}", s.showTask)
	s.mux.HandleFunc("GET /products", s.listProducts)
	s.mux.HandleFunc("GET /worktrees", s.listWorktrees)
	s.mux.HandleFunc("PUT /tasks/{key}/status", s.setTaskStatus)
	s.mux.HandleFunc("POST /worktrees", s.registerWorktree)
	s.mux.HandleFunc("PUT /sessions/{id}/handoff", s.saveHandoff)
}

// writerSession resolves the explicit session_id every flow write carries.
func (s *Server) writerSession(r *http.Request, id string) (store.Session, error) {
	if id == "" {
		return store.Session{}, &store.Error{Code: "required_field", Message: "session_id is required"}
	}
	return s.st.GetSession(r.Context(), id)
}

func (s *Server) setTaskStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.writerSession(r, in.SessionID); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Status == "" {
		s.fail(w, r, &store.Error{Code: "required_field", Message: "status is required"})
		return
	}
	res, err := s.st.SetTaskStatus(r.Context(), r.PathValue("key"), in.Status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) registerWorktree(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
		Repo      string `json:"repo"`
		Branch    string `json:"branch"`
		Base      string `json:"base"`
		Purpose   string `json:"purpose"`
		TaskID    string `json:"task_id"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.writerSession(r, in.SessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for name, v := range map[string]string{"path": in.Path, "repo": in.Repo, "branch": in.Branch} {
		if v == "" {
			s.fail(w, r, &store.Error{Code: "required_field", Message: name + " is required"})
			return
		}
	}
	if !filepath.IsAbs(in.Path) {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "path " + strconv.Quote(in.Path) + " must be absolute"})
		return
	}
	task := in.TaskID
	switch {
	case task == "" && sess.PinnedAt == "":
		s.fail(w, r, &store.Error{Code: "no_pin", Message: "the session has no pinned task and the call carries no task_id"})
		return
	case task == "":
		task = sess.TaskID
	case strings.EqualFold(task, store.NoTaskKey):
		task = ""
	}
	res, err := s.st.UpsertWorktree(r.Context(), store.NewWorktree{Path: in.Path, Repo: in.Repo, Branch: in.Branch,
		Base: in.Base, Purpose: in.Purpose, Task: task, SessionID: sess.ID})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}

func (s *Server) saveHandoff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		TaskID  string `json:"task_id"`
		Title   string `json:"title"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.st.SaveHandoff(r.Context(), store.Handoff{
		SessionID: r.PathValue("id"), TaskID: in.TaskID, Title: in.Title, Content: in.Content})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var taskID *string
	if o := res.Observation; o.TaskID != "" {
		taskID = &o.TaskID
	}
	status := http.StatusCreated
	if res.Replaced {
		status = http.StatusOK
	}
	o := res.Observation
	writeJSON(w, status, map[string]any{
		"sync_id": o.SyncID, "type": o.Type, "kind": o.Kind, "title": o.Title, "task_id": taskID,
		"task_key": res.TaskKey, "session_id": o.SessionID, "project": o.Project,
		"replaced": res.Replaced, "footer_rewritten": res.FooterRewritten, "created_at": o.CreatedAt,
	})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	if v.Get("where") != "" {
		s.fail(w, r, &store.Error{Code: "invalid_field", Message: "where: there are no custom fields yet"})
		return
	}
	q := store.TaskQuery{Statuses: multi(v["status"]), Product: v.Get("product"), Parent: v.Get("parent")}
	tree, err := boolParam(v.Get("tree"), false, "tree")
	if err == nil {
		q.RootsOnly, err = boolParam(v.Get("roots_only"), false, "roots_only")
	}
	if err == nil {
		q.IncludeDone, err = boolParam(v.Get("include_done"), false, "include_done")
	}
	if err == nil {
		q.Limit, q.Offset, err = page(v.Get("limit"), v.Get("offset"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tasks, total, err := s.st.ListTasks(r.Context(), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := map[string]any{"total": total, "limit": q.Limit, "offset": q.Offset, "tree": tree, "tasks": tasks}
	if tree {
		if body["tasks"], err = s.nest(r, q, tasks); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// taskNode is a GET /tasks row under tree=true; children is [] on leaves.
type taskNode struct {
	store.TaskSummary
	Children []taskNode `json:"children"`
}

// nest hangs each task's direct children under it, with the status filter of
// the request but not its product or paging.
func (s *Server) nest(r *http.Request, q store.TaskQuery, tasks []store.TaskSummary) ([]taskNode, error) {
	nodes := make([]taskNode, len(tasks))
	for i, t := range tasks {
		nodes[i] = taskNode{TaskSummary: t, Children: []taskNode{}}
		if t.ChildrenCount == 0 {
			continue
		}
		sub := store.TaskQuery{Statuses: q.Statuses, IncludeDone: q.IncludeDone, Parent: t.TaskKey, Limit: store.MaxLimit}
		children, _, err := s.st.ListTasks(r.Context(), sub)
		if err != nil {
			return nil, err
		}
		if nodes[i].Children, err = s.nest(r, sub, children); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (s *Server) showTask(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := store.TaskShowQuery{ObservationsLimit: store.DefaultObservationsLimit}
	var err error
	q.IncludeSubtasks, err = boolParam(v.Get("include_subtasks"), true, "include_subtasks")
	if err == nil && v.Get("observations_limit") != "" {
		q.ObservationsLimit, err = intParam(v.Get("observations_limit"), "observations_limit")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.st.GetTask(r.Context(), r.PathValue("key"), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := store.ProductQuery{Q: v.Get("q")}
	var err error
	if q.Limit, q.Offset, err = page(v.Get("limit"), v.Get("offset")); err != nil {
		s.fail(w, r, err)
		return
	}
	products, total, err := s.st.ListProducts(r.Context(), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "limit": q.Limit, "offset": q.Offset, "products": products})
}

func (s *Server) listWorktrees(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := store.WorktreeQuery{Task: v.Get("task_id"), Repo: v.Get("repo")}
	var err error
	if q.AliveOnly, err = boolParam(v.Get("alive"), false, "alive"); err == nil {
		q.Limit, q.Offset, err = page(v.Get("limit"), v.Get("offset"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	wts, total, err := s.st.ListWorktrees(r.Context(), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "limit": q.Limit, "offset": q.Offset, "worktrees": wts})
}
