package server

import (
	"bytes"
	"encoding/json"
	"net/http"
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

func TestSaveHandoffErrors(t *testing.T) {
	f := newFixture(t)
	f.flowError("PUT", "/sessions/nope/handoff", map[string]string{"content": "x"}, 404, "unknown_session")
	f.flowError("PUT", "/sessions/s2/handoff", map[string]string{"content": "x"}, 422, "no_pin")
	f.flowError("PUT", "/sessions/s1/handoff", map[string]string{"content": "x", "task_id": "NOPE-1"}, 404, "unknown_task")
	f.flowError("PUT", "/sessions/s1/handoff", map[string]string{}, 400, "invalid_request")
}
