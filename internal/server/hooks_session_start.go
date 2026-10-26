package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/rcrespodev/neugram/internal/hooks"
	"github.com/rcrespodev/neugram/internal/project"
	"github.com/rcrespodev/neugram/internal/store"
)

var sessionStartSources = []string{"startup", "resume", "clear", "compact", "fork"}

func (s *Server) hookSessionStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Source    string `json:"source"`
	}
	err := decode(w, r, &req)
	switch {
	case err != nil:
	case req.SessionID == "" || req.Cwd == "":
		err = &store.Error{Code: "invalid_request", Message: "session_id and cwd are required"}
	case !filepath.IsAbs(req.Cwd):
		err = &store.Error{Code: "invalid_request", Message: "cwd must be an absolute path"}
	case !slices.Contains(sessionStartSources, req.Source):
		err = &store.Error{Code: "invalid_request", Message: "source must be one of startup, resume, clear, compact, fork"}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	dctx, cancel := context.WithTimeout(ctx, detectTimeout)
	res, err := project.Detect(dctx, req.Cwd)
	cancel()
	if err != nil {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "cannot detect project: " + err.Error()})
		return
	}
	sess, created, err := s.st.UpsertSession(ctx, req.SessionID, res.Project, req.Cwd)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	overrides, err := s.gateOverrides(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	view := hooks.StartView{SessionID: sess.ID, Instructions: renderGenerated(overrides), Pinned: sess.PinnedAt != ""}
	worktreesAlive, openTasks := 0, 0
	var pin any

	var pinKey string
	if view.Pinned {
		pinKey = store.NoTaskKey
		if sess.TaskID != "" {
			if pinKey, err = s.st.TaskKeyByID(ctx, sess.TaskID); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		view.PinKey = pinKey
		pin = map[string]string{"task_key": pinKey}
	}
	taskPinned := view.Pinned && pinKey != store.NoTaskKey
	if taskPinned {
		d, err := s.st.GetTask(ctx, pinKey, store.TaskShowQuery{})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		view.PinTitle, view.PinStatus = d.Task.Title, d.Task.Status
		pin = map[string]string{"task_key": pinKey, "status": d.Task.Status}
		for _, wt := range d.Worktrees {
			view.Worktrees = append(view.Worktrees, hooks.StartWorktree{Path: wt.Path, Branch: wt.Branch, Alive: wt.Alive})
			if wt.Alive {
				worktreesAlive++
			}
		}
	}
	if !view.Pinned {
		open, total, err := s.st.ListTasks(ctx, store.TaskQuery{Limit: store.MaxLimit})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		view.OpenTotal, openTasks = total, total
		for _, t := range open {
			st := hooks.StartTask{Key: t.TaskKey, Title: t.Title, Status: t.Status}
			if t.Product != nil {
				st.Product = *t.Product
			}
			view.OpenTasks = append(view.OpenTasks, st)
		}
		if req.Source == "clear" {
			hint, err := s.st.LastPinnedSessionByDirectory(ctx, req.Cwd, sess.ID)
			if err == nil {
				view.LastPin = hint.TaskKey
			} else if !isNotFound(err) {
				s.fail(w, r, err)
				return
			}
		}
	}

	var note store.HandoffNote
	var handoffSyncID any
	switch {
	case taskPinned:
		note, err = s.st.LastHandoff(ctx, pinKey)
	case !view.Pinned:
		note, err = s.st.LastHandoffByDirectory(ctx, req.Cwd)
		view.HandoffIsHint = true
	default:
		err = store.ErrNotFound
	}
	switch {
	case err == nil:
		view.Handoff = &hooks.StartHandoff{Title: note.Observation.Title, Content: note.Observation.Content}
		handoffSyncID = note.Observation.SyncID
	case !isNotFound(err):
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stdout": hooks.RenderSessionStart(view), "session_created": created, "project": sess.Project,
		"pin": pin, "open_tasks": openTasks, "handoff_sync_id": handoffSyncID, "worktrees_alive": worktreesAlive,
	})
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
