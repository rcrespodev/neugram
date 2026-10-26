package server

import (
	"net/http"

	"github.com/rcrespodev/neugram/internal/store"
)

func (s *Server) routesFlow() {
	s.mux.HandleFunc("GET /tasks", s.listTasks)
	s.mux.HandleFunc("GET /tasks/{key}", s.showTask)
	s.mux.HandleFunc("GET /products", s.listProducts)
	s.mux.HandleFunc("GET /worktrees", s.listWorktrees)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	for _, unsupported := range []string{"tree", "where"} {
		if v.Get(unsupported) != "" && v.Get(unsupported) != "false" {
			s.fail(w, r, &store.Error{Code: "invalid_request", Message: unsupported + " is not supported yet"})
			return
		}
	}
	q := store.TaskQuery{Statuses: multi(v["status"]), Product: v.Get("product"), Parent: v.Get("parent")}
	var err error
	if q.RootsOnly, err = boolParam(v.Get("roots_only"), false, "roots_only"); err == nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "limit": q.Limit, "offset": q.Offset, "tree": false, "tasks": tasks})
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
