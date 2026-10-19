package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rcrespodev/neugram/internal/server"
	"github.com/rcrespodev/neugram/internal/store"
)

func startServer(t *testing.T, extraTasks int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neugram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const ts = "2026-10-01T10:00:00Z"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO products (id, name, created_at, updated_at) VALUES ('P1', 'Microservices', ?1, ?1)`, ts)
	exec(`INSERT INTO tasks (id, key, title, product_id, status, created_at, updated_at) VALUES
	  ('T1', 'TRIPULSE-1', 'Best Skills', 'P1', 'in-progress', ?1, ?1),
	  ('T2', 'OLD-1', 'Closed one', NULL, 'done', ?1, ?1)`, ts)
	exec(`INSERT INTO sessions (id, project, directory, started_at, task_id, pinned_at, pinned_by, created_at, updated_at)
	  VALUES ('s1', 'neugram', '/repo', ?1, 'T1', ?1, 'import', ?1, ?1)`, ts)
	exec(`INSERT INTO worktrees (id, task_id, path, repo, branch, base, purpose, created_at, updated_at)
	  VALUES ('W1', 'T1', '/nonexistent/wt', 'neugram', 'feat/x', 'main', 'the purpose', ?1, ?1)`, ts)
	exec(`INSERT INTO observations (sync_id, session_id, type, title, content, project, task_id, kind, created_at, updated_at) VALUES
	  ('obs-h', 's1', 'handoff', 'Handoff TRIPULSE-1: listo', 'x', 'neugram', 'T1', 'handoff', ?1, ?1)`, ts)
	for i := range extraTasks {
		exec(`INSERT INTO tasks (id, key, title, status, created_at, updated_at) VALUES (?, ?, 'bulk', 'todo', ?, ?)`,
			fmt.Sprintf("B%03d", i), fmt.Sprintf("BULK-%03d", i), ts, ts)
	}
	srv := httptest.NewServer(server.New(st, server.Options{Version: buildVersion(), DBPath: path}).Handler())
	t.Cleanup(srv.Close)
	t.Setenv("NEUGRAM_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	noStart(t)
}

func noStart(t *testing.T) {
	t.Helper()
	prev := startDaemon
	startDaemon = func(string) error { return errors.New("start disabled in tests") }
	t.Cleanup(func() { startDaemon = prev })
}

func run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestTaskLsPrintsOpenTasks(t *testing.T) {
	startServer(t, 0)
	code, out, errOut := run("task", "ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "TAREA") || lines[2] != "1 tareas" {
		t.Fatalf("stdout = %q", out)
	}
	if f := strings.Fields(lines[1]); f[0] != "TRIPULSE-1" || f[1] != "in-progress" || f[2] != "Microservices" {
		t.Fatalf("row = %q", lines[1])
	}
}

func TestTaskLsAllAndStatusFilters(t *testing.T) {
	startServer(t, 0)
	if _, out, _ := run("task", "ls", "--all"); !strings.Contains(out, "OLD-1") || !strings.Contains(out, "2 tareas") {
		t.Fatalf("--all stdout = %q", out)
	}
	if _, out, _ := run("task", "ls", "--status", "done"); !strings.Contains(out, "OLD-1") || strings.Contains(out, "TRIPULSE-1") {
		t.Fatalf("--status stdout = %q", out)
	}
	code, _, errOut := run("task", "ls", "--status", "closed")
	if code != 1 || !strings.Contains(errOut, "invalid_enum") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestTaskLsJSONPagesThroughEveryTask(t *testing.T) {
	startServer(t, store.MaxLimit+5)
	code, out, errOut := run("task", "ls", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Total int                 `json:"total"`
		Tasks []store.TaskSummary `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != store.MaxLimit+6 || len(got.Tasks) != got.Total || got.Tasks[0].TaskKey != "BULK-000" {
		t.Fatalf("total %d, rows %d", got.Total, len(got.Tasks))
	}
}

func TestTaskShowPrintsSessionsWorktreesAndHandoffs(t *testing.T) {
	startServer(t, 0)
	code, out, errOut := run("task", "show", "tripulse-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"TRIPULSE-1 — Best Skills", "producto   Microservices",
		"sesiones (1)", "s1", "pin:import",
		"worktrees (1, 0 vivos)", "muerto  /nonexistent/wt", "feat/x ← main",
		"handoffs (1)", "obs-h", "Handoff TRIPULSE-1: listo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestTaskShowUnknownTaskFails(t *testing.T) {
	startServer(t, 0)
	code, _, errOut := run("task", "show", "NOPE-1")
	if code != 1 || !strings.Contains(errOut, "unknown_task") || !strings.Contains(errOut, "neugram task ls") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestTaskShowNeedsOneKey(t *testing.T) {
	startServer(t, 0)
	if code, _, _ := run("task", "show"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if code, _, _ := run("task", "show", "A", "B"); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestTaskWtAcceptsFlagsAfterTheKey(t *testing.T) {
	startServer(t, 0)
	code, out, errOut := run("task", "wt", "TRIPULSE-1", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Worktrees []store.Worktree `json:"worktrees"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Worktrees) != 1 || got.Worktrees[0].Alive || got.Worktrees[0].Path != "/nonexistent/wt" {
		t.Fatalf("worktrees = %+v", got.Worktrees)
	}
	if _, out, _ := run("task", "wt", "OLD-1"); !strings.Contains(out, "OLD-1 no tiene worktrees registrados") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestProductLs(t *testing.T) {
	startServer(t, 0)
	code, out, errOut := run("product", "ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Microservices") || !strings.Contains(out, "1 productos") {
		t.Fatalf("stdout = %q", out)
	}
	if code, _, _ := run("product", "rm"); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestUnknownTaskSubcommandFails(t *testing.T) {
	if code, _, errOut := run("task", "nope"); code != 2 || !strings.Contains(errOut, `unknown task command "nope"`) {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestDaemonDownAndUnstartableFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	t.Setenv("NEUGRAM_ADDR", addr)
	noStart(t)
	code, _, errOut := run("task", "ls")
	if code != 1 || !strings.Contains(errOut, "no pude lanzar neugram serve") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestDaemonOfAnotherVersionIsReplaced(t *testing.T) {
	var version atomic.Value
	version.Store("old")
	var shutdowns, starts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		v := version.Load().(string)
		if v == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"version": v})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		shutdowns.Add(1)
		version.Store("")
	})
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total": 0, "products": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("NEUGRAM_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	prev := startDaemon
	startDaemon = func(string) error {
		starts.Add(1)
		version.Store(buildVersion())
		return nil
	}
	t.Cleanup(func() { startDaemon = prev })

	code, _, errOut := run("product", "ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if shutdowns.Load() != 1 || starts.Load() != 1 {
		t.Fatalf("shutdowns %d, starts %d", shutdowns.Load(), starts.Load())
	}
}

func TestNeugramHomeMovesTheDefaultDatabase(t *testing.T) {
	t.Setenv("NEUGRAM_HOME", "/tmp/nh")
	if got := defaultDB(); got != "/tmp/nh/neugram.db" {
		t.Fatalf("defaultDB = %q", got)
	}
}

func TestDaemonReplacedByAnotherClientBeforeThePollCounts(t *testing.T) {
	var version atomic.Value
	version.Store("old")
	var starts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"version": version.Load().(string)})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		version.Store(buildVersion())
	})
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total": 0, "products": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("NEUGRAM_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	prev := startDaemon
	startDaemon = func(string) error {
		starts.Add(1)
		return nil
	}
	t.Cleanup(func() { startDaemon = prev })

	if code, _, errOut := run("product", "ls"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if starts.Load() != 0 {
		t.Fatalf("starts = %d", starts.Load())
	}
}
