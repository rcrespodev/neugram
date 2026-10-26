package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rcrespodev/neugram/internal/store"
)

type fixture struct {
	t         *testing.T
	srv       *Server
	http      *httptest.Server
	alivePath string
	deadPath  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "neugram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{t: t, alivePath: filepath.Join(dir, "wt-alive"), deadPath: filepath.Join(dir, "wt-gone")}
	if err := os.Mkdir(f.alivePath, 0o700); err != nil {
		t.Fatal(err)
	}
	seed(t, path, f.alivePath, f.deadPath)
	f.srv = New(st, Options{Version: "test-1", DBPath: path})
	f.http = httptest.NewServer(f.srv.Handler())
	t.Cleanup(f.http.Close)
	return f
}

func seed(t *testing.T, path, alive, dead string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const ts = "2026-10-01T10:00:00Z"
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO products (id, name, description, created_at, updated_at) VALUES
		  ('P1', 'Microservices', 'core', ?1, ?1), ('P2', 'Portal', NULL, ?1, ?1)`, []any{ts}},
		{`INSERT INTO tasks (id, key, title, product_id, parent_task_id, status, tracker_kind, tracker_id, tracker_url, notes, created_at, updated_at) VALUES
		  ('T1', 'EPIC-1', 'Epic', 'P1', NULL, 'in-progress', 'clickup', 'abc', 'https://x/abc', 'n', ?1, ?1),
		  ('T2', 'EPIC-1-a', 'Child', 'P1', 'T1', 'todo', NULL, NULL, NULL, NULL, ?1, ?1),
		  ('T3', 'EPIC-1-a-x', 'Grandchild', 'P1', 'T2', 'review', NULL, NULL, NULL, NULL, ?1, ?1),
		  ('T4', 'DONE-1', 'Closed', 'P2', NULL, 'done', NULL, NULL, NULL, NULL, ?1, ?1),
		  ('T5', 'zeta', 'Lowercase', NULL, NULL, 'todo', NULL, NULL, NULL, NULL, ?1, ?1)`, []any{ts}},
		{`INSERT INTO sessions (id, project, directory, started_at, ended_at, task_id, pinned_at, pinned_by, created_at, updated_at) VALUES
		  ('s1', 'neugram', '/repo', '2026-10-01T09:00:00Z', '2026-10-01T11:00:00Z', 'T1', ?1, 'import', ?1, ?1),
		  ('s2', 'neugram', '/repo', '2026-10-02T09:00:00Z', NULL, NULL, NULL, NULL, ?1, ?1)`, []any{ts}},
		{`INSERT INTO worktrees (id, task_id, path, repo, branch, base, purpose, created_by_session, created_at, updated_at) VALUES
		  ('W1', 'T1', ?2, 'neugram', 'feat/a', 'main', 'alive one', 's1', '2026-10-01T10:00:00Z', ?1),
		  ('W2', 'T1', ?3, 'neugram', 'feat/b', NULL, NULL, 'ghost', '2026-10-01T09:00:00Z', ?1)`, []any{ts, alive, dead}},
		{`INSERT INTO observations (sync_id, session_id, type, title, content, project, task_id, kind, created_at, updated_at, deleted_at) VALUES
		  ('obs-1', 's1', 'decision', 'Epic decision', 'body one', 'neugram', 'T1', NULL, '2026-10-01T10:01:00Z', ?1, NULL),
		  ('obs-2', 's1', 'handoff', 'Handoff EPIC-1', 'handoff body', 'neugram', 'T1', 'handoff', '2026-10-01T10:02:00Z', ?1, NULL),
		  ('obs-3', 's1', 'discovery', 'Grandchild finding', 'body three', 'neugram', 'T3', NULL, '2026-10-01T10:03:00Z', ?1, NULL),
		  ('obs-4', 's1', 'decision', 'Deleted', 'gone', 'neugram', 'T1', NULL, '2026-10-01T10:04:00Z', ?1, ?1)`, []any{ts}},
		{`INSERT INTO progress_reports (id, session_id, delegation_id, task_id, role, status, title, summary, next_steps, created_at, updated_at) VALUES
		  ('R1', 's1', 'impl-1', 'T1', 'impl', 'done', 'Progress EPIC-1 impl-1', '[]', '[]', ?1, ?1)`, []any{ts}},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
}

func (f *fixture) get(path string, want int, out any) {
	f.t.Helper()
	resp, err := http.Get(f.http.URL + path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		f.t.Fatalf("GET %s: status %d, want %d", path, resp.StatusCode, want)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		f.t.Fatalf("GET %s: content-type %q", path, ct)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		f.t.Fatal(err)
	}
}

type taskList struct {
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
	Tasks  []store.TaskSummary `json:"tasks"`
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Details struct {
			OpenTasks []string `json:"open_tasks"`
		} `json:"details"`
	} `json:"error"`
}

func keys(tasks []store.TaskSummary) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.TaskKey
	}
	return out
}

func TestHealthReportsVersionAndSchema(t *testing.T) {
	f := newFixture(t)
	var h map[string]any
	f.get("/health", http.StatusOK, &h)
	if h["status"] != "ok" || h["version"] != "test-1" || h["schema_version"] != float64(1) {
		t.Fatalf("health = %v", h)
	}
}

func TestListTasksHidesDoneAndOrdersByKeyBytewise(t *testing.T) {
	f := newFixture(t)
	var got taskList
	f.get("/tasks", http.StatusOK, &got)
	if want := []string{"EPIC-1", "EPIC-1-a", "EPIC-1-a-x", "zeta"}; !slices.Equal(keys(got.Tasks), want) {
		t.Fatalf("keys = %v, want %v", keys(got.Tasks), want)
	}
	if got.Total != 4 || got.Limit != store.DefaultLimit || got.Offset != 0 {
		t.Fatalf("envelope = %d/%d/%d", got.Total, got.Limit, got.Offset)
	}
	epic := got.Tasks[0]
	if epic.SessionsCount != 1 || epic.WorktreesAlive != 1 || epic.ObservationsCount != 2 || epic.ChildrenCount != 1 {
		t.Fatalf("epic counters = %+v", epic)
	}
	if epic.Tracker == nil || epic.Tracker.ID != "abc" || *epic.Product != "Microservices" || epic.ParentKey != nil {
		t.Fatalf("epic = %+v", epic)
	}
	if epic.LastObservationAt == nil || *epic.LastObservationAt != "2026-10-01T10:02:00Z" {
		t.Fatalf("last_observation_at = %v", epic.LastObservationAt)
	}
	grand := got.Tasks[2]
	if !slices.Equal(grand.AncestorKeys, []string{"EPIC-1", "EPIC-1-a"}) || *grand.ParentKey != "EPIC-1-a" {
		t.Fatalf("grandchild = %+v", grand)
	}
	if got.Tasks[3].Product != nil || len(got.Tasks[3].AncestorKeys) != 0 {
		t.Fatalf("zeta = %+v", got.Tasks[3])
	}
}

func TestListTasksFilters(t *testing.T) {
	f := newFixture(t)
	cases := map[string][]string{
		"/tasks?status=done":             {"DONE-1"},
		"/tasks?status=todo,review":      {"EPIC-1-a", "EPIC-1-a-x", "zeta"},
		"/tasks?status=todo&status=done": {"DONE-1", "EPIC-1-a", "zeta"},
		"/tasks?include_done=true":       {"DONE-1", "EPIC-1", "EPIC-1-a", "EPIC-1-a-x", "zeta"},
		"/tasks?product=microservices":   {"EPIC-1", "EPIC-1-a", "EPIC-1-a-x"},
		"/tasks?parent=epic-1":           {"EPIC-1-a"},
		"/tasks?parent=T2":               {"EPIC-1-a-x"},
		"/tasks?roots_only=true":         {"EPIC-1", "zeta"},
		"/tasks?product=nope":            {},
	}
	for path, want := range cases {
		var got taskList
		f.get(path, http.StatusOK, &got)
		if !slices.Equal(keys(got.Tasks), want) || got.Total != len(want) {
			t.Errorf("%s: keys = %v (total %d), want %v", path, keys(got.Tasks), got.Total, want)
		}
	}
}

func TestListTasksPages(t *testing.T) {
	f := newFixture(t)
	var got taskList
	f.get("/tasks?limit=2&offset=2", http.StatusOK, &got)
	if got.Total != 4 || !slices.Equal(keys(got.Tasks), []string{"EPIC-1-a-x", "zeta"}) {
		t.Fatalf("page = %v (total %d)", keys(got.Tasks), got.Total)
	}
}

func TestListTasksRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		path   string
		status int
		code   string
	}{
		{"/tasks?status=closed", http.StatusUnprocessableEntity, "invalid_enum"},
		{"/tasks?limit=0", http.StatusBadRequest, "invalid_request"},
		{"/tasks?limit=201", http.StatusBadRequest, "invalid_request"},
		{"/tasks?offset=-1", http.StatusBadRequest, "invalid_request"},
		{"/tasks?include_done=maybe", http.StatusBadRequest, "invalid_request"},
		{"/tasks?tree=maybe", http.StatusBadRequest, "invalid_request"},
		{"/tasks?where=impact", http.StatusUnprocessableEntity, "invalid_field"},
		{"/tasks?parent=NOPE", http.StatusNotFound, "unknown_task"},
	}
	for _, c := range cases {
		var e errorBody
		f.get(c.path, c.status, &e)
		if e.Error.Code != c.code || e.Error.Message == "" {
			t.Errorf("%s: error = %+v", c.path, e.Error)
		}
	}
}

func TestShowTaskReturnsTheWholeCard(t *testing.T) {
	f := newFixture(t)
	var d store.TaskDetail
	f.get("/tasks/epic-1", http.StatusOK, &d)
	if d.Task.TaskKey != "EPIC-1" || d.Task.Notes == nil || *d.Task.Notes != "n" || d.Task.Tracker.URL != "https://x/abc" {
		t.Fatalf("task = %+v", d.Task)
	}
	if d.Children == nil || len(*d.Children) != 1 {
		t.Fatalf("children = %v", d.Children)
	}
	child := (*d.Children)[0]
	if child.TaskKey != "EPIC-1-a" || len(child.Children) != 1 || child.Children[0].TaskKey != "EPIC-1-a-x" || child.Children[0].ObservationsCount != 1 {
		t.Fatalf("child = %+v", child)
	}
	if len(d.Sessions) != 1 || d.Sessions[0].SessionID != "s1" || *d.Sessions[0].PinnedBy != "import" {
		t.Fatalf("sessions = %+v", d.Sessions)
	}
	if len(d.Worktrees) != 2 || !d.Worktrees[0].Alive || d.Worktrees[1].Alive || d.Worktrees[1].Base != nil {
		t.Fatalf("worktrees = %+v", d.Worktrees)
	}
	if len(d.Handoffs) != 1 || d.Handoffs[0].SyncID != "obs-2" {
		t.Fatalf("handoffs = %+v", d.Handoffs)
	}
	if len(d.ProgressReports) != 1 || d.ProgressReports[0].DelegationID != "impl-1" {
		t.Fatalf("progress = %+v", d.ProgressReports)
	}
	var obs []string
	for _, o := range d.Observations {
		obs = append(obs, o.SyncID+"@"+o.TaskKey)
	}
	if !slices.Equal(obs, []string{"obs-3@EPIC-1-a-x", "obs-1@EPIC-1"}) {
		t.Fatalf("observations = %v", obs)
	}
}

func TestShowTaskWithoutSubtasks(t *testing.T) {
	f := newFixture(t)
	var raw map[string]json.RawMessage
	f.get("/tasks/EPIC-1?include_subtasks=false&observations_limit=1", http.StatusOK, &raw)
	if _, ok := raw["children"]; ok {
		t.Fatalf("children present: %s", raw["children"])
	}
	var obs []store.ObservationPreview
	json.Unmarshal(raw["observations"], &obs)
	if len(obs) != 1 || obs[0].SyncID != "obs-1" || obs[0].Preview != "body one" {
		t.Fatalf("observations = %+v", obs)
	}
}

func TestShowUnknownTaskListsOpenTasks(t *testing.T) {
	f := newFixture(t)
	for _, key := range []string{"NOPE-9", "none"} {
		var e errorBody
		f.get("/tasks/"+key, http.StatusNotFound, &e)
		if e.Error.Code != "unknown_task" || e.Error.Hint == "" {
			t.Fatalf("%s: error = %+v", key, e.Error)
		}
		if want := []string{"EPIC-1 — Epic", "EPIC-1-a — Child", "EPIC-1-a-x — Grandchild", "zeta — Lowercase"}; !slices.Equal(e.Error.Details.OpenTasks, want) {
			t.Fatalf("%s: open_tasks = %v", key, e.Error.Details.OpenTasks)
		}
	}
	var e errorBody
	f.get("/tasks/EPIC-1?observations_limit=101", http.StatusBadRequest, &e)
}

func TestListWorktrees(t *testing.T) {
	f := newFixture(t)
	var got struct {
		Total     int              `json:"total"`
		Worktrees []store.Worktree `json:"worktrees"`
	}
	f.get("/worktrees?task_id=EPIC-1", http.StatusOK, &got)
	if got.Total != 2 || got.Worktrees[0].Path != f.alivePath || *got.Worktrees[0].TaskKey != "EPIC-1" {
		t.Fatalf("worktrees = %+v", got)
	}
	f.get("/worktrees?alive=true", http.StatusOK, &got)
	if got.Total != 1 || !got.Worktrees[0].Alive {
		t.Fatalf("alive = %+v", got)
	}
	f.get("/worktrees?task_id=zeta", http.StatusOK, &got)
	if got.Total != 0 || got.Worktrees == nil {
		t.Fatalf("zeta = %+v", got)
	}
	var e errorBody
	f.get("/worktrees?task_id=NOPE", http.StatusNotFound, &e)
}

func TestListProducts(t *testing.T) {
	f := newFixture(t)
	var got struct {
		Total    int             `json:"total"`
		Products []store.Product `json:"products"`
	}
	f.get("/products", http.StatusOK, &got)
	if got.Total != 2 || got.Products[0].Name != "Microservices" || got.Products[0].TasksCount != 3 || got.Products[0].OpenTasksCount != 3 {
		t.Fatalf("products = %+v", got)
	}
	if p := got.Products[1]; p.Name != "Portal" || p.TasksCount != 1 || p.OpenTasksCount != 0 || p.Description != nil {
		t.Fatalf("portal = %+v", p)
	}
	f.get("/products?q=port", http.StatusOK, &got)
	if got.Total != 1 || got.Products[0].Name != "Portal" {
		t.Fatalf("q = %+v", got)
	}
}

func TestShutdownSignalsStopping(t *testing.T) {
	f := newFixture(t)
	for range 2 {
		resp, err := http.Post(f.http.URL+"/shutdown", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	}
	select {
	case <-f.srv.Stopping():
	default:
		t.Fatal("Stopping not closed")
	}
}

func TestRunLeavesTheDatabaseAloneWhenThePortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	db := filepath.Join(t.TempDir(), "neugram.db")
	err = Run(context.Background(), ln.Addr().String(), Options{Version: "v", DBPath: db}, nil)
	if !errors.Is(err, ErrAddrInUse) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("database touched: %v", err)
	}
}

func TestRunServesUntilShutdown(t *testing.T) {
	db := filepath.Join(t.TempDir(), "neugram.db")
	addr := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), "127.0.0.1:0", Options{Version: "v9", DBPath: db}, func(a net.Addr) { addr <- a.String() })
	}()
	base := "http://" + <-addr
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if h["version"] != "v9" || h["db"] != db {
		t.Fatalf("health = %v", h)
	}
	resp, err = http.Post(base+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after /shutdown")
	}
}

func TestForeignHostIsRejected(t *testing.T) {
	f := newFixture(t)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.http.URL, "http://"))
	cases := map[string]int{
		"evil.example":         http.StatusBadRequest,
		"evil.example:" + port: http.StatusBadRequest,
		"127.0.0.1:1":          http.StatusBadRequest,
		"localhost:" + port:    http.StatusOK,
		"127.0.0.1:" + port:    http.StatusOK,
		"[::1]:" + port:        http.StatusOK,
	}
	for host, want := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/tasks"
			if method == http.MethodPost {
				path = "/shutdown"
			}
			req, _ := http.NewRequest(method, f.http.URL+path, nil)
			req.Host = host
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var e errorBody
			json.NewDecoder(resp.Body).Decode(&e)
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("%s %s with Host %q: status %d, want %d", method, path, host, resp.StatusCode, want)
			}
			if want != http.StatusOK && e.Error.Code != "invalid_request" {
				t.Errorf("Host %q: error = %+v", host, e.Error)
			}
		}
	}
	select {
	case <-f.srv.Stopping():
	default:
		t.Fatal("loopback POST /shutdown did not stop")
	}
}
