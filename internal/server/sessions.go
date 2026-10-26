package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rcrespodev/neugram/internal/project"
	"github.com/rcrespodev/neugram/internal/store"
)

const (
	detectTimeout = 2 * time.Second
	maxBodyBytes  = 1 << 20
)

func (s *Server) routesSessions() {
	s.mux.HandleFunc("POST /sessions", s.createSession)
	s.mux.HandleFunc("GET /sessions/{id}", s.getSession)
	s.mux.HandleFunc("POST /sessions/{id}/pin", s.pinSession)
	s.mux.HandleFunc("POST /sessions/{id}/end", s.endSession)
	s.mux.HandleFunc("POST /prompts", s.addPrompt)
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return &store.Error{Code: "invalid_request", Message: "body must be a JSON object: " + err.Error()}
	}
	return nil
}

type sessionPin struct {
	TaskID   string `json:"task_id"`
	TaskKey  string `json:"task_key"`
	PinnedAt string `json:"pinned_at"`
	PinnedBy string `json:"pinned_by"`
}

func (s *Server) pinOf(ctx context.Context, sess store.Session) (*sessionPin, error) {
	if sess.PinnedAt == "" {
		return nil, nil
	}
	pin := &sessionPin{TaskID: sess.TaskID, TaskKey: store.NoTaskKey, PinnedAt: sess.PinnedAt, PinnedBy: sess.PinnedBy}
	if sess.TaskID != "" {
		key, err := s.st.TaskKeyByID(ctx, sess.TaskID)
		if err != nil {
			return nil, err
		}
		pin.TaskKey = key
	}
	return pin, nil
}

// getSession serves identity and pin; counts, handoff, observations and
// prompts of the UI body in docs/specs/api.md are not served yet.
func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pin, err := s.pinOf(r.Context(), sess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var ended *string
	if sess.EndedAt != "" {
		ended = &sess.EndedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sess.ID, "project": sess.Project, "directory": sess.Directory,
		"started_at": sess.StartedAt, "ended_at": ended, "pin": pin,
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string `json:"id"`
		Project   string `json:"project"`
		Directory string `json:"directory"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.ID == "" || in.Directory == "" {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "id and directory are required"})
		return
	}
	if !filepath.IsAbs(in.Directory) {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "directory must be an absolute path"})
		return
	}
	if in.Project == "" {
		ctx, cancel := context.WithTimeout(r.Context(), detectTimeout)
		res, err := project.Detect(ctx, in.Directory)
		cancel()
		if err != nil {
			s.fail(w, r, &store.Error{Code: "invalid_request", Message: "cannot detect project: " + err.Error()})
			return
		}
		in.Project = res.Project
	}
	sess, created, err := s.st.UpsertSession(r.Context(), in.ID, in.Project, in.Directory)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pin, err := s.pinOf(r.Context(), sess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"session_id": sess.ID, "project": sess.Project, "directory": sess.Directory,
		"started_at": sess.StartedAt, "created": created, "pin": pin,
	})
}

func (s *Server) pinSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TaskID   string         `json:"task_id"`
		Title    string         `json:"title"`
		Product  string         `json:"product"`
		Parent   string         `json:"parent"`
		Tracker  *store.Tracker `json:"tracker"`
		Notes    string         `json:"notes"`
		PinnedBy string         `json:"pinned_by"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.st.PinSession(r.Context(), store.PinRequest{
		SessionID: r.PathValue("id"), Target: in.TaskID, PinnedBy: in.PinnedBy, Title: in.Title,
		Product: in.Product, Parent: in.Parent, Tracker: in.Tracker, Notes: in.Notes,
	})
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

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if err := s.st.EndSession(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addPrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
		Project   string `json:"project"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.SessionID == "" {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "session_id is required"})
		return
	}
	p, deduped, err := s.st.AddPrompt(r.Context(), in.SessionID, in.Content, in.Project)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"sync_id": p.SyncID, "session_id": p.SessionID, "project": p.Project, "created_at": p.CreatedAt, "deduped": deduped,
	})
}
