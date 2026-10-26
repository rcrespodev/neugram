// Package server serves the HTTP API on :7438 and the local web UI.
package server

import (
	"encoding/json"
	"net"
	"net/http"
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
	s.routesMemory()
	s.routesFlow()
	s.routesHooks()
	s.routesSessions()
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("POST /shutdown", s.shutdown)
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
