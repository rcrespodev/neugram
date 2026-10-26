package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rcrespodev/neugram/internal/client"
	"github.com/rcrespodev/neugram/internal/server"
	"github.com/rcrespodev/neugram/internal/store"
)

func realDaemon(t *testing.T) *sdk.ClientSession {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neugram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.UpsertSession(context.Background(), "s1", "neugram", "/repo"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(st, server.Options{Version: "t", DBPath: path}).Handler())
	t.Cleanup(srv.Close)
	c := client.New("t")
	c.Addr = strings.TrimPrefix(srv.URL, "http://")
	return connect(t, &Server{client: c, ensure: func() error { return nil }})
}

func callJSON(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (bool, map[string]any) {
	t.Helper()
	res, text := callText(t, cs, name, args)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: %q", name, text)
	}
	return res.IsError, out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func pinNew(t *testing.T, cs *sdk.ClientSession, key string, extra map[string]any) {
	t.Helper()
	args := map[string]any{"session_id": "s1", "task_id": key, "title": "T " + key}
	for k, v := range extra {
		args[k] = v
	}
	if isErr, out := callJSON(t, cs, "task_pin", args); isErr {
		t.Fatalf("pin %s: %v", key, out)
	}
}

func TestTaskPinWithTitleCreatesAndPins(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_pin", map[string]any{"session_id": "s1", "task_id": "EPIC-1", "title": "Epic"})
	if isErr || out["task_key"] != "EPIC-1" || out["created"] != true {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskPinUnknownKeyWithoutTitleIsUnknownTask(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_pin", map[string]any{"session_id": "s1", "task_id": "NOPE-1"})
	if !isErr || errCode(out) != "unknown_task" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskListAndShowReturnTheTask(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	isErr, list := callJSON(t, cs, "task_list", nil)
	if isErr || !strings.Contains(toJSON(list), `"EPIC-1"`) {
		t.Fatalf("list: %v", list)
	}
	isErr, show := callJSON(t, cs, "task_show", map[string]any{"task_id": "EPIC-1"})
	if isErr || !strings.Contains(toJSON(show), `"EPIC-1"`) {
		t.Fatalf("show: %v", show)
	}
}

func TestTaskStatusDoneWithOpenChildrenReportsThem(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	pinNew(t, cs, "EPIC-1-a", map[string]any{"parent": "EPIC-1"})
	isErr, out := callJSON(t, cs, "task_status", map[string]any{"session_id": "s1", "task_id": "EPIC-1", "status": "done"})
	if isErr || out["status"] != "done" || out["open_children"] != float64(1) {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestHandoffSaveTwiceReplaces(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	args := map[string]any{"session_id": "s1", "content": "## State\nfirst"}
	_, first := callJSON(t, cs, "handoff_save", args)
	args["content"] = "## State\nsecond"
	_, second := callJSON(t, cs, "handoff_save", args)
	if first["replaced"] != false || second["replaced"] != true || first["sync_id"] != second["sync_id"] {
		t.Fatalf("first=%v second=%v", first, second)
	}
}

func TestWorktreeRegisterRelativePathIsInvalidRequest(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	isErr, out := callJSON(t, cs, "worktree_register", map[string]any{"session_id": "s1", "path": "rel/wt", "repo": "neugram", "branch": "b"})
	if !isErr || errCode(out) != "invalid_request" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskKeyWithReservedCharactersIsEscapedInThePath(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "A B?#%2F", nil)
	isErr, out := callJSON(t, cs, "task_show", map[string]any{"task_id": "A B?#%2F"})
	if isErr || !strings.Contains(toJSON(out), `A B?#%2F`) {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskPinForwardsTrackerNotesAndProduct(t *testing.T) {
	cs := realDaemon(t)
	_, out := callJSON(t, cs, "task_pin", map[string]any{
		"session_id": "s1", "task_id": "EPIC-1", "title": "Epic", "notes": "some notes",
		"tracker": "clickup:86dxyz:https://app.clickup.com/t/86dxyz",
	})
	if out["created"] != true {
		t.Fatalf("pin: %v", out)
	}
	_, show := callJSON(t, cs, "task_show", map[string]any{"task_id": "EPIC-1"})
	task, _ := show["task"].(map[string]any)
	tracker, _ := task["tracker"].(map[string]any)
	if task["notes"] != "some notes" || tracker["kind"] != "clickup" || tracker["id"] != "86dxyz" || tracker["url"] != "https://app.clickup.com/t/86dxyz" {
		t.Fatalf("task = %v", task)
	}
}

func TestTaskPinMalformedTrackerIsInvalidRequest(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_pin", map[string]any{"session_id": "s1", "task_id": "EPIC-1", "title": "E", "tracker": "clickup"})
	if !isErr || errCode(out) != "invalid_request" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskPinProductOfAnUnknownProductIsRejected(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_pin", map[string]any{"session_id": "s1", "task_id": "EPIC-1", "title": "E", "product": "Nope"})
	if !isErr {
		t.Fatalf("out=%v", out)
	}
}

func TestTaskListTreeNestsChildren(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	pinNew(t, cs, "EPIC-1-a", map[string]any{"parent": "EPIC-1"})
	isErr, out := callJSON(t, cs, "task_list", map[string]any{"tree": true, "roots_only": true})
	tasks, _ := out["tasks"].([]any)
	if isErr || out["tree"] != true || len(tasks) != 1 {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
	children, _ := tasks[0].(map[string]any)["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("children = %v", children)
	}
}

func TestTaskListWhereReachesTheDaemon(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_list", map[string]any{"where": map[string]any{"nope": "x"}})
	if !isErr || errCode(out) != "invalid_field" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskShowIncludeSubtasksAndObservationsLimit(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	pinNew(t, cs, "EPIC-1-a", map[string]any{"parent": "EPIC-1"})
	_, with := callJSON(t, cs, "task_show", map[string]any{"task_id": "EPIC-1"})
	_, without := callJSON(t, cs, "task_show", map[string]any{"task_id": "EPIC-1", "include_subtasks": false})
	if len(with["children"].([]any)) != 1 {
		t.Fatalf("with = %v", with)
	}
	if c, _ := without["children"].([]any); len(c) != 0 {
		t.Fatalf("without = %v", without)
	}
	if isErr, out := callJSON(t, cs, "task_show", map[string]any{"task_id": "EPIC-1", "observations_limit": 101}); !isErr {
		t.Fatalf("limit 101 accepted: %v", out)
	}
}

func TestWorktreeRegisterCreatesThenUpdates(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	args := map[string]any{"session_id": "s1", "path": "/tmp/wt-a", "repo": "neugram", "branch": "feat/x", "base": "main", "purpose": "work"}
	isErr, first := callJSON(t, cs, "worktree_register", args)
	if isErr || first["created"] != true || first["task_key"] != "EPIC-1" {
		t.Fatalf("first: %v", first)
	}
	args["branch"] = "feat/y"
	_, second := callJSON(t, cs, "worktree_register", args)
	if second["created"] != false || second["branch"] != "feat/y" {
		t.Fatalf("second: %v", second)
	}
}

func TestHandoffSaveWithoutSessionIsInvalidRequest(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "handoff_save", map[string]any{"content": "x"})
	if !isErr || errCode(out) != "invalid_request" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskStatusWithoutTaskIdUsesThePin(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	isErr, out := callJSON(t, cs, "task_status", map[string]any{"session_id": "s1", "status": "review"})
	if isErr || out["task_key"] != "EPIC-1" || out["status"] != "review" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskShowWithoutTaskIdUsesThePin(t *testing.T) {
	cs := realDaemon(t)
	pinNew(t, cs, "EPIC-1", nil)
	isErr, out := callJSON(t, cs, "task_show", map[string]any{"session_id": "s1"})
	if isErr || !strings.Contains(toJSON(out), `"EPIC-1"`) {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskStatusWithoutPinIsNoPin(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_status", map[string]any{"session_id": "s1", "status": "done"})
	if !isErr || errCode(out) != "no_pin" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskStatusWithNonePinIsNoPinWithAHint(t *testing.T) {
	cs := realDaemon(t)
	if isErr, out := callJSON(t, cs, "task_pin", map[string]any{"session_id": "s1", "task_id": "none"}); isErr {
		t.Fatal(out)
	}
	isErr, out := callJSON(t, cs, "task_status", map[string]any{"session_id": "s1", "status": "done"})
	hint, _ := out["error"].(map[string]any)["hint"].(string)
	if !isErr || errCode(out) != "no_pin" || !strings.Contains(hint, "no task") {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskShowWithoutTaskIdNorSessionIsInvalidRequest(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_show", nil)
	if !isErr || errCode(out) != "invalid_request" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}

func TestTaskStatusWithUnknownSessionIsUnknownSession(t *testing.T) {
	cs := realDaemon(t)
	isErr, out := callJSON(t, cs, "task_status", map[string]any{"session_id": "ghost", "status": "done"})
	if !isErr || errCode(out) != "unknown_session" {
		t.Fatalf("isErr=%v out=%v", isErr, out)
	}
}
