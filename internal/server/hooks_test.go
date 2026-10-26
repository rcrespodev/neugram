package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type hookTool struct {
	name  string
	input map[string]any
}

func writeTranscript(t *testing.T, tools ...hookTool) string {
	t.Helper()
	var b strings.Builder
	emit := func(v any) {
		line, _ := json.Marshal(v)
		b.Write(line)
		b.WriteByte('\n')
	}
	emit(map[string]any{"type": "user", "cwd": "/home/r/proj", "message": map[string]any{"role": "user", "content": "hacé algo"}})
	for i, tl := range tools {
		id := "toolu_" + string(rune('a'+i))
		emit(map[string]any{"type": "assistant", "cwd": "/home/r/proj", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": tl.name, "input": tl.input},
		}}})
		emit(map[string]any{"type": "user", "cwd": "/home/r/proj", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"},
		}}})
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func editTool(path string) hookTool { return hookTool{"Edit", map[string]any{"file_path": path}} }

func saveTool() hookTool {
	return hookTool{"mcp__neugram__mem_save", map[string]any{"content": "x"}}
}

func bashTool(cmd string) hookTool { return hookTool{"Bash", map[string]any{"command": cmd}} }

type stopBody struct {
	Decision       *string  `json:"decision"`
	Reason         string   `json:"reason"`
	Gate           *string  `json:"gate"`
	GatesEvaluated []string `json:"gates_evaluated"`
	Observed       struct {
		FilesMutated     []string `json:"files_mutated"`
		Saves            int      `json:"saves"`
		WorktreesCreated []string `json:"worktrees_created"`
		ProgressReports  int      `json:"progress_reports"`
	} `json:"observed"`
}

func (f *fixture) stop(route, session, path string, active bool) stopBody {
	f.t.Helper()
	var out stopBody
	f.post(route, map[string]any{"session_id": session, "transcript_path": path, "stop_hook_active": active}, http.StatusOK, &out)
	return out
}

func TestStopBlocksMutationWithoutPin(t *testing.T) {
	f := newFixture(t)
	out := f.stop("/hooks/stop", "s2", writeTranscript(t, editTool("/home/r/proj/a.go")), false)
	if out.Decision == nil || *out.Decision != "block" || out.Gate == nil || *out.Gate != "require-task-pin-on-mutation" {
		t.Fatalf("out = %+v", out)
	}
	for _, want := range []string{"EPIC-1 — Epic [in-progress]", "zeta — Lowercase [todo]", `task_pin {"session_id": "s2", "task_id": "none"}`} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("reason sin %q:\n%s", want, out.Reason)
		}
	}
	if strings.Contains(out.Reason, "DONE-1") {
		t.Error("lista una tarea cerrada")
	}
	if len(out.GatesEvaluated) != 1 {
		t.Fatalf("gates_evaluated = %v", out.GatesEvaluated)
	}
}

func TestStopPinnedNoneAndSavePasses(t *testing.T) {
	f := newFixture(t)
	f.post("/sessions/s2/pin", map[string]string{"task_id": "none"}, http.StatusOK, nil)
	out := f.stop("/hooks/stop", "s2", writeTranscript(t, editTool("/home/r/proj/a.go"), saveTool()), false)
	if out.Decision != nil || out.Gate != nil {
		t.Fatalf("out = %+v", out)
	}
	if len(out.GatesEvaluated) != 3 || out.Observed.Saves != 1 || len(out.Observed.FilesMutated) != 1 || out.Observed.FilesMutated[0] != "/home/r/proj/a.go" {
		t.Fatalf("out = %+v", out)
	}
}

func TestStopBlocksMutationWithoutSave(t *testing.T) {
	f := newFixture(t)
	out := f.stop("/hooks/stop", "s1", writeTranscript(t, editTool("/home/r/proj/a.go")), false)
	if out.Gate == nil || *out.Gate != "require-save-on-mutation" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.GatesEvaluated) != 2 {
		t.Fatalf("gates_evaluated = %v", out.GatesEvaluated)
	}
}

func TestStopWorktreeRegistration(t *testing.T) {
	f := newFixture(t)
	register := hookTool{"mcp__neugram__worktree_register", map[string]any{"path": "/home/r/wt-x"}}
	cases := []struct {
		name  string
		tools []hookTool
		block bool
	}{
		{"sin register", []hookTool{bashTool("git worktree add /home/r/wt-x"), saveTool()}, true},
		{"con register", []hookTool{bashTool("git worktree add /home/r/wt-x"), saveTool(), register}, false},
		{"no expandible sin register", []hookTool{bashTool(`git worktree add "$HOME/wt-x"`), saveTool()}, true},
		{"no expandible con register", []hookTool{bashTool(`git worktree add "$HOME/wt-x"`), saveTool(), register}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := f.stop("/hooks/stop", "s1", writeTranscript(t, c.tools...), false)
			if blocked := out.Gate != nil && *out.Gate == "require-worktree-registered"; blocked != c.block {
				t.Fatalf("out = %+v", out)
			}
			if c.block && strings.Contains(c.name, "expandible") && !strings.Contains(out.Reason, `$HOME/wt-x`) {
				t.Fatalf("reason sin el comando: %s", out.Reason)
			}
		})
	}
}

func TestSubagentStopSkipsSaveAndReadsEntireTranscript(t *testing.T) {
	f := newFixture(t)
	out := f.stop("/hooks/subagent-stop", "s1", writeTranscript(t, editTool("/home/r/proj/a.go")), false)
	if out.Decision != nil || strings.Join(out.GatesEvaluated, ",") != "require-task-pin-on-mutation,require-worktree-registered" {
		t.Fatalf("out = %+v", out)
	}
	out = f.stop("/hooks/subagent-stop", "s2", writeTranscript(t, editTool("/home/r/proj/a.go")), false)
	if out.Gate == nil || *out.Gate != "require-task-pin-on-mutation" {
		t.Fatalf("out = %+v", out)
	}
}

func TestGatesRowDisablesGate(t *testing.T) {
	f := newFixture(t)
	db, err := sql.Open("sqlite", f.srv.opts.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO gates (name, enabled, updated_at) VALUES ('require-save-on-mutation', 0, '2026-10-01T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	out := f.stop("/hooks/stop", "s1", writeTranscript(t, editTool("/home/r/proj/a.go")), false)
	if out.Decision != nil || strings.Contains(strings.Join(out.GatesEvaluated, ","), "require-save-on-mutation") {
		t.Fatalf("out = %+v", out)
	}
}

func TestStopHookActiveEvaluatesNothing(t *testing.T) {
	f := newFixture(t)
	out := f.stop("/hooks/stop", "s2", writeTranscript(t, editTool("/home/r/proj/a.go")), true)
	if out.Decision != nil || len(out.GatesEvaluated) != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestStopErrors(t *testing.T) {
	f := newFixture(t)
	path := writeTranscript(t)
	f.post("/hooks/stop", map[string]any{"session_id": "ghost", "transcript_path": path}, http.StatusNotFound, nil)
	f.post("/hooks/stop", map[string]any{"session_id": "s1", "transcript_path": filepath.Join(t.TempDir(), "nope.jsonl")}, http.StatusBadRequest, nil)
	f.post("/hooks/stop", map[string]any{"session_id": "s1"}, http.StatusBadRequest, nil)
}

func TestStopRejectsUnsafeTranscriptPaths(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	notJSONL := filepath.Join(dir, "t.txt")
	big := filepath.Join(dir, "big.jsonl")
	fifo := filepath.Join(dir, "pipe.jsonl")
	dirPath := filepath.Join(dir, "dir.jsonl")
	if err := os.WriteFile(notJSONL, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bf, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := bf.Truncate(maxTranscriptBytes + 1); err != nil {
		t.Fatal(err)
	}
	bf.Close()
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"relativo": "rel/t.jsonl", "otra extension": notJSONL, "demasiado grande": big,
		"fifo": fifo, "directorio": dirPath, "inexistente": filepath.Join(dir, "none.jsonl"),
	}
	for _, route := range []string{"/hooks/stop", "/hooks/subagent-stop"} {
		for name, p := range cases {
			t.Run(route+" "+name, func(t *testing.T) {
				f.post(route, map[string]any{"session_id": "s1", "transcript_path": p}, http.StatusBadRequest, nil)
				f.post(route, map[string]any{"session_id": "s1", "transcript_path": writeTranscript(t), "agent_transcript_path": p}, http.StatusBadRequest, nil)
			})
		}
	}
}

func TestSubagentStopReadsAgentTranscriptPath(t *testing.T) {
	f := newFixture(t)
	parent := writeTranscript(t)
	agent := writeTranscript(t, editTool("/home/r/proj/a.go"))
	var out stopBody
	f.post("/hooks/subagent-stop", map[string]any{"session_id": "s2", "transcript_path": parent, "agent_transcript_path": agent}, http.StatusOK, &out)
	if out.Gate == nil || *out.Gate != "require-task-pin-on-mutation" {
		t.Fatalf("out = %+v", out)
	}
}
