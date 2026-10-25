// Package server serves the HTTP API on :7438 and the local web UI.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rcrespodev/neugram/internal/store"
)

type Options struct {
	Version string
	DBPath  string
	LogPath string

	afterStoreClose func()
	wrapHandler     func(http.Handler) http.Handler
}

// Server is the daemon's HTTP surface; the store it wraps is the only
// connection to neugram.db (docs/specs/api.md).
type Server struct {
	st       *store.Store
	opts     Options
	started  time.Time
	mux      *http.ServeMux
	stopOnce sync.Once
	stop     chan struct{}
}

func New(st *store.Store, opts Options) *Server {
	s := &Server{st: st, opts: opts, started: time.Now(), mux: http.NewServeMux(), stop: make(chan struct{})}
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("POST /shutdown", s.shutdown)
	s.mux.HandleFunc("GET /tasks", s.listTasks)
	s.mux.HandleFunc("GET /tasks/{key}", s.showTask)
	s.mux.HandleFunc("GET /products", s.listProducts)
	s.mux.HandleFunc("GET /worktrees", s.listWorktrees)
	return s
}

// Handler only answers requests addressed to loopback by name or IP on the
// bound port, which keeps a DNS-rebound page from reading or stopping it.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r) {
			writeJSON(w, http.StatusBadRequest, map[string]apiError{"error": {
				Code: "invalid_request", Message: "host " + strconv.Quote(r.Host) + " is not this daemon",
				Hint: "usá http://127.0.0.1:<puerto> o http://localhost:<puerto>",
			}})
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func loopbackHost(r *http.Request) bool {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		return false
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	_, localPort, err := net.SplitHostPort(local.String())
	if err != nil || port != localPort {
		return false
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// Stopping is closed when a client asked for POST /shutdown.
func (s *Server) Stopping() <-chan struct{} { return s.stop }

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	schema, _ := strconv.Atoi(store.SchemaVersion)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        s.opts.Version,
		"db":             s.opts.DBPath,
		"schema_version": schema,
		"uptime_s":       int(time.Since(s.started).Seconds()),
	})
}

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
	s.stopOnce.Do(func() { close(s.stop) })
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

// multi accepts both ?status=a&status=b and ?status=a,b.
func multi(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func boolParam(raw string, def bool, name string) (bool, error) {
	if raw == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, &store.Error{Code: "invalid_request", Message: name + " must be true or false"}
	}
	return b, nil
}

func intParam(raw, name string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &store.Error{Code: "invalid_request", Message: name + " must be an integer"}
	}
	return n, nil
}

func page(limit, offset string) (l, o int, err error) {
	l = store.DefaultLimit
	if limit != "" {
		if l, err = intParam(limit, "limit"); err != nil {
			return 0, 0, err
		}
		if l == 0 {
			return 0, 0, &store.Error{Code: "invalid_request", Message: "limit must be between 1 and 200"}
		}
	}
	if offset != "" {
		o, err = intParam(offset, "offset")
	}
	return l, o, err
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

var statusByCode = map[string]int{
	"invalid_request": http.StatusBadRequest,
	"unknown_task":    http.StatusNotFound,
	"unknown_session": http.StatusNotFound,
	"not_found":       http.StatusNotFound,
	"invalid_enum":    http.StatusUnprocessableEntity,
	"invalid_type":    http.StatusUnprocessableEntity,
}

// fail writes the error envelope of docs/specs/api.md; unknown_task carries
// the open tasks so the caller can pick one.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *store.Error
	if !errors.As(err, &se) {
		s.logInternal(r, err)
		writeJSON(w, http.StatusInternalServerError, map[string]apiError{"error": {
			Code: "internal", Message: err.Error(), Hint: "mirá " + s.opts.LogPath,
		}})
		return
	}
	e := apiError{Code: se.Code, Message: se.Message}
	switch se.Code {
	case "unknown_task":
		e.Hint = "neugram task ls"
		if open, _, err := s.st.ListTasks(r.Context(), store.TaskQuery{Limit: store.MaxLimit}); err == nil {
			names := make([]string, len(open))
			for i, t := range open {
				names[i] = t.TaskKey + " — " + t.Title
			}
			e.Details = map[string]any{"open_tasks": names}
		}
	case "invalid_enum":
		e.Hint = "status ∈ " + strings.Join(store.TaskStatuses, " · ")
	}
	status, ok := statusByCode[se.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]apiError{"error": e})
}

func (s *Server) logInternal(r *http.Request, err error) {
	if s.opts.LogPath == "" {
		return
	}
	f, ferr := os.OpenFile(s.opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s serve %s %s internal: %v\n", time.Now().UTC().Format(store.TimeLayout), r.Method, r.URL.Path, err)
}
