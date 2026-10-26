package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func (f *fixture) flowSend(method, path string, body any, want int, out any) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var e errorBody
		json.NewDecoder(resp.Body).Decode(&e)
		f.t.Fatalf("%s %s: status %d, want %d (%+v)", method, path, resp.StatusCode, want, e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) flowError(method, path string, body any, want int, code string) {
	f.t.Helper()
	var e errorBody
	f.flowSend(method, path, body, want, &e)
	if e.Error.Code != code {
		f.t.Fatalf("%s %s: code %q, want %q", method, path, e.Error.Code, code)
	}
}

func TestSetTaskStatus(t *testing.T) {
	f := newFixture(t)
	var got struct {
		TaskKey        string `json:"task_key"`
		Status         string `json:"status"`
		PreviousStatus string `json:"previous_status"`
	}
	f.flowSend("PUT", "/tasks/EPIC-1-a/status", map[string]string{"session_id": "s1", "status": "in-progress"}, 200, &got)
	if got.TaskKey != "EPIC-1-a" || got.Status != "in-progress" || got.PreviousStatus != "todo" {
		t.Fatalf("got %+v", got)
	}
}

func TestSetTaskStatusErrors(t *testing.T) {
	f := newFixture(t)
	f.flowError("PUT", "/tasks/EPIC-1/status", map[string]string{"status": "done"}, 422, "required_field")
	f.flowError("PUT", "/tasks/EPIC-1/status", map[string]string{"session_id": "nope", "status": "done"}, 404, "unknown_session")
	f.flowError("PUT", "/tasks/NOPE-1/status", map[string]string{"session_id": "s1", "status": "done"}, 404, "unknown_task")
	f.flowError("PUT", "/tasks/EPIC-1/status", map[string]string{"session_id": "s1", "status": "wat"}, 422, "invalid_enum")
}

func TestSetTaskStatusDoneWithOpenChildrenWarns(t *testing.T) {
	f := newFixture(t)
	var got struct {
		Status       string `json:"status"`
		OpenChildren int    `json:"open_children"`
	}
	f.flowSend("PUT", "/tasks/EPIC-1/status", map[string]string{"session_id": "s1", "status": "done"}, 200, &got)
	if got.Status != "done" || got.OpenChildren != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestRegisterWorktreeCreatesThenUpdates(t *testing.T) {
	f := newFixture(t)
	body := map[string]string{"session_id": "s1", "path": f.alivePath + "-new", "repo": "neugram", "branch": "feat/x", "base": "main"}
	var got struct {
		Created          bool    `json:"created"`
		TaskKey          *string `json:"task_key"`
		CreatedBySession *string `json:"created_by_session"`
	}
	f.flowSend("POST", "/worktrees", body, 201, &got)
	if !got.Created || got.TaskKey == nil || *got.TaskKey != "EPIC-1" || got.CreatedBySession == nil || *got.CreatedBySession != "s1" {
		t.Fatalf("got %+v", got)
	}
	body["branch"] = "feat/y"
	f.flowSend("POST", "/worktrees", body, 200, &got)
	if got.Created {
		t.Fatal("second registration reported created")
	}
}

func TestRegisterWorktreeErrors(t *testing.T) {
	f := newFixture(t)
	ok := func() map[string]string {
		return map[string]string{"session_id": "s1", "path": f.alivePath + "-e", "repo": "neugram", "branch": "b"}
	}
	rel := ok()
	rel["path"] = "relative/wt"
	f.flowError("POST", "/worktrees", rel, 400, "invalid_request")
	noSession := ok()
	delete(noSession, "session_id")
	f.flowError("POST", "/worktrees", noSession, 422, "required_field")
	ghost := ok()
	ghost["session_id"] = "nope"
	f.flowError("POST", "/worktrees", ghost, 404, "unknown_session")
	unpinned := ok()
	unpinned["session_id"] = "s2"
	f.flowError("POST", "/worktrees", unpinned, 422, "no_pin")
	badTask := ok()
	badTask["task_id"] = "NOPE-1"
	f.flowError("POST", "/worktrees", badTask, 404, "unknown_task")
}

func TestSaveHandoffTwiceReplaces(t *testing.T) {
	f := newFixture(t)
	var first, second struct {
		SyncID   string `json:"sync_id"`
		Type     string `json:"type"`
		Kind     string `json:"kind"`
		TaskKey  string `json:"task_key"`
		Replaced bool   `json:"replaced"`
	}
	f.post("/sessions", map[string]string{"id": "fresh", "directory": t.TempDir()}, 201, nil)
	f.post("/sessions/fresh/pin", map[string]string{"task_id": "EPIC-1"}, 200, nil)
	f.flowSend("PUT", "/sessions/fresh/handoff", map[string]string{"content": "## State\nuno"}, 201, &first)
	if first.Replaced || first.Type != "handoff" || first.Kind != "handoff" || first.TaskKey != "EPIC-1" {
		t.Fatalf("first %+v", first)
	}
	f.flowSend("PUT", "/sessions/fresh/handoff", map[string]string{"content": "## State\ndos"}, 200, &second)
	if !second.Replaced || second.SyncID != first.SyncID {
		t.Fatalf("second %+v", second)
	}
}

type taskTree struct {
	Tree  bool `json:"tree"`
	Tasks []struct {
		TaskKey  string `json:"task_key"`
		Children []struct {
			TaskKey  string `json:"task_key"`
			Children []struct {
				TaskKey  string            `json:"task_key"`
				Children []json.RawMessage `json:"children"`
			} `json:"children"`
		} `json:"children"`
	} `json:"tasks"`
}

func TestListTasksTreeNestsThreeLevels(t *testing.T) {
	f := newFixture(t)
	var got taskTree
	f.get("/tasks?tree=true&roots_only=true", http.StatusOK, &got)
	if !got.Tree || len(got.Tasks) != 2 {
		t.Fatalf("got %+v", got)
	}
	epic := got.Tasks[0]
	if epic.TaskKey != "EPIC-1" || len(epic.Children) != 1 || epic.Children[0].TaskKey != "EPIC-1-a" {
		t.Fatalf("epic %+v", epic)
	}
	leaf := epic.Children[0].Children
	if len(leaf) != 1 || leaf[0].TaskKey != "EPIC-1-a-x" || leaf[0].Children == nil || len(leaf[0].Children) != 0 {
		t.Fatalf("leaf %+v", leaf)
	}
}

type instructionsBody struct {
	Product   *string `json:"product"`
	Source    string  `json:"source"`
	Generated string  `json:"generated"`
	Free      string  `json:"free"`
	Rendered  *string `json:"rendered"`
}

func TestInstructionsListEnabledGatesAndTypes(t *testing.T) {
	f := newFixture(t)
	var got instructionsBody
	f.get("/instructions", http.StatusOK, &got)
	for _, want := range []string{"require-task-pin-on-mutation", "require-save-on-mutation", "require-worktree-registered",
		"session_id", "task_pin", "handoff_save", "worktree_register", "mem_judge",
		"bugfix", "decision", "architecture", "discovery", "pattern", "config", "preference", "handoff", "session_summary"} {
		if !strings.Contains(got.Generated, want) {
			t.Errorf("generated lacks %q", want)
		}
	}
	for _, off := range []string{"block-topic-key-collision", "require-field", "require-progress-report"} {
		if strings.Contains(got.Generated, off) {
			t.Errorf("generated lists disabled gate %q", off)
		}
	}
	if got.Source != "global" || got.Free != "" || got.Rendered == nil || *got.Rendered != got.Generated {
		t.Fatalf("got %+v", got)
	}
}

func TestInstructionsHonourGateRows(t *testing.T) {
	f := newFixture(t)
	db, err := sql.Open("sqlite", f.srv.opts.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO gates (name, enabled, updated_at) VALUES
		('require-save-on-mutation', 0, '2026-10-01T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var got instructionsBody
	f.get("/instructions?rendered=false", http.StatusOK, &got)
	if strings.Contains(got.Generated, "require-save-on-mutation") || !strings.Contains(got.Generated, "require-worktree-registered") {
		t.Fatalf("generated = %s", got.Generated)
	}
	if got.Rendered != nil {
		t.Fatal("rendered=false still returned rendered")
	}
}

func TestInstructionsProduct(t *testing.T) {
	f := newFixture(t)
	var got instructionsBody
	f.get("/instructions?product=portal", http.StatusOK, &got)
	if got.Product == nil || *got.Product != "portal" {
		t.Fatalf("got %+v", got)
	}
	var e errorBody
	f.get("/instructions?product=Nope", http.StatusNotFound, &e)
	if e.Error.Code != "unknown_product" {
		t.Fatalf("error %+v", e.Error)
	}
	f.get("/instructions?rendered=maybe", http.StatusBadRequest, &e)
}

func TestSaveHandoffErrors(t *testing.T) {
	f := newFixture(t)
	f.flowError("PUT", "/sessions/nope/handoff", map[string]string{"content": "x"}, 404, "unknown_session")
	f.flowError("PUT", "/sessions/s2/handoff", map[string]string{"content": "x"}, 422, "no_pin")
	f.flowError("PUT", "/sessions/s1/handoff", map[string]string{"content": "x", "task_id": "NOPE-1"}, 404, "unknown_task")
	f.flowError("PUT", "/sessions/s1/handoff", map[string]string{}, 400, "invalid_request")
}
