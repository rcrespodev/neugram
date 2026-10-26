//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type env struct {
	bin, home, addr, repo string
	vars                  []string
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "neugram")
	build := exec.Command("go", "build", "-o", bin, "github.com/rcrespodev/neugram/cmd/neugram")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	e := &env{bin: bin, home: filepath.Join(root, "home"), addr: freeAddr(t), repo: repo}
	e.vars = append(os.Environ(), "NEUGRAM_HOME="+e.home, "NEUGRAM_ADDR="+e.addr)
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/shutdown", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	return e
}

type hookResult struct {
	stdout, stderr string
	code           int
}

func (e *env) hook(t *testing.T, event string, in map[string]any) hookResult {
	t.Helper()
	raw, _ := json.Marshal(in)
	cmd := exec.Command(e.bin, "hook", event)
	cmd.Env = e.vars
	cmd.Stdin = bytes.NewReader(raw)
	var out, errw bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errw
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("hook %s: %v", event, err)
	}
	return hookResult{out.String(), errw.String(), code}
}

func (e *env) mcp(t *testing.T) *sdk.ClientSession {
	t.Helper()
	cmd := exec.Command(e.bin, "mcp")
	cmd.Env = e.vars
	cl := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	sess, err := cl.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func call(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: result is not JSON: %q", tool, text)
	}
	return out, res.IsError
}

func mustCall(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	out, isErr := call(t, s, tool, args)
	if isErr {
		t.Fatalf("%s failed: %v", tool, out)
	}
	return out
}

type tline map[string]any

func humanLine(cwd, text string) tline {
	return tline{"type": "user", "cwd": cwd, "message": map[string]any{"role": "user", "content": text}, "origin": map[string]any{"kind": "human"}}
}

func toolUse(cwd, id, name string, input map[string]any) tline {
	return tline{"type": "assistant", "cwd": cwd, "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
}

func toolResult(cwd, id, content string) tline {
	return tline{"type": "user", "cwd": cwd, "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}}}}
}

func writeTranscript(t *testing.T, lines ...tline) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		raw, _ := json.Marshal(l)
		b.Write(raw)
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFullSession(t *testing.T) {
	e := newEnv(t)
	sidA, sidB := "e2e-a-0001", "e2e-b-0002"
	const task = "E2E-1"
	edit := toolUse(e.repo, "t1", "Edit", map[string]any{"file_path": "/srv/e2e/main.go"})
	editOK := toolResult(e.repo, "t1", "ok")

	r := e.hook(t, "session-start", map[string]any{"session_id": sidA, "cwd": e.repo, "source": "startup"})
	if r.code != 0 {
		t.Fatalf("session-start: code %d stderr %q", r.code, r.stderr)
	}
	first, _, _ := strings.Cut(r.stdout, "\n")
	if first != "CLAUDE_CODE_SESSION_ID="+sidA {
		t.Fatalf("first line = %q", first)
	}
	low := strings.ToLower(r.stdout)
	if !strings.Contains(low, "sin pin") && !strings.Contains(low, "no pin") {
		t.Errorf("session-start does not say the session has no pin:\n%s", r.stdout)
	}

	tr := writeTranscript(t, humanLine(e.repo, "editá main.go"), edit, editOK)
	r = e.hook(t, "stop", map[string]any{"session_id": sidA, "transcript_path": tr, "cwd": e.repo})
	if r.code != 0 {
		t.Fatalf("stop: code %d", r.code)
	}
	var block struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(r.stdout), &block); err != nil {
		t.Fatalf("stop stdout is not JSON: %q", r.stdout)
	}
	if block.Decision != "block" || !strings.Contains(block.Reason, "task_pin") {
		t.Fatalf("stop did not block with task_pin: %+v", block)
	}

	m := e.mcp(t)
	pin := mustCall(t, m, "task_pin", map[string]any{"task_id": task, "title": "Ensayo e2e", "session_id": sidA})
	if pin["created"] != true || pin["task_key"] != task || pin["session_id"] != sidA {
		t.Errorf("task_pin: %v", pin)
	}
	shown := mustCall(t, m, "task_show", map[string]any{"task_id": task})
	if !strings.Contains(fmt.Sprint(shown), "Ensayo e2e") {
		t.Errorf("task_show does not show the created task: %v", shown)
	}

	tr = writeTranscript(t, humanLine(e.repo, "editá main.go"), edit, editOK,
		toolUse(e.repo, "t2", "mcp__neugram__mem_save", map[string]any{"title": "Edita main", "content": "x", "type": "discovery", "session_id": sidA}),
		toolResult(e.repo, "t2", `{"sync_id":"x"}`))
	r = e.hook(t, "stop", map[string]any{"session_id": sidA, "transcript_path": tr, "cwd": e.repo})
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("stop with pin and save: code %d stdout %q", r.code, r.stdout)
	}

	content := "What: el daemon cierra la transaccion de escritura antes de responder.\nWhy: evitar lecturas sucias en el cliente.\nWhere: internal/server/memory.go\n"
	first1 := mustCall(t, m, "mem_save", map[string]any{"title": "Cierra la transaccion antes de responder", "content": content, "type": "decision", "session_id": sidA})
	if first1["judgment_required"] == true {
		t.Errorf("first mem_save should not need judgment: %v", first1)
	}
	second := mustCall(t, m, "mem_save", map[string]any{"title": "Cierra la transaccion antes de responder", "content": content + "Learned: el orden importa.\n", "type": "decision", "session_id": sidA})
	if second["judgment_required"] != true {
		t.Fatalf("second mem_save: no judgment_required: %v", second)
	}
	cands, _ := second["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatalf("second mem_save has no candidates: %v", second)
	}
	cand, _ := cands[0].(map[string]any)
	jid, _ := cand["judgment_id"].(string)
	if jid == "" {
		t.Fatalf("candidate without judgment_id: %v", cand)
	}
	judged := mustCall(t, m, "mem_judge", map[string]any{"judgment_id": jid, "relation": "related", "reason": "misma decision, mas detalle", "session_id": sidA})
	if judged["judgment_status"] != "judged" || judged["relation"] != "related" {
		t.Errorf("mem_judge: %v", judged)
	}

	handoff := "## Goal\nProbar el flujo e2e\n## Next Steps\nContinuar con el ensayo\n"
	mustCall(t, m, "handoff_save", map[string]any{"session_id": sidA, "content": handoff, "title": "Handoff e2e"})
	r = e.hook(t, "session-end", map[string]any{"session_id": sidA, "reason": "other"})
	if r.code != 0 {
		t.Fatalf("session-end: code %d stderr %q", r.code, r.stderr)
	}

	r = e.hook(t, "session-start", map[string]any{"session_id": sidB, "cwd": e.repo, "source": "startup"})
	first, _, _ = strings.Cut(r.stdout, "\n")
	if r.code != 0 || first != "CLAUDE_CODE_SESSION_ID="+sidB {
		t.Fatalf("second session-start: code %d out %q", r.code, r.stdout)
	}
	if !strings.Contains(r.stdout, task) || !strings.Contains(r.stdout, "Probar el flujo e2e") {
		t.Errorf("startup does not carry the pin hint and the previous handoff:\n%s", r.stdout)
	}
	mustCall(t, m, "task_pin", map[string]any{"task_id": task, "session_id": sidB})
	r = e.hook(t, "session-start", map[string]any{"session_id": sidB, "cwd": e.repo, "source": "resume"})
	if r.code != 0 || !strings.Contains(r.stdout, "Continuar con el ensayo") || !strings.Contains(r.stdout, "Probar el flujo e2e") {
		t.Errorf("resume does not carry the whole handoff:\n%s", r.stdout)
	}

	resp, err := http.Post("http://"+e.addr+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitDown(t, e.addr)
	squatter, err := net.Listen("tcp", e.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	r = e.hook(t, "stop", map[string]any{"session_id": sidB, "transcript_path": tr, "cwd": e.repo})
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("stop with the daemon down: code %d stdout %q", r.code, r.stdout)
	}
	log, err := os.ReadFile(filepath.Join(e.home, "hooks.log"))
	if err != nil || !strings.Contains(string(log), "daemon_unreachable") {
		t.Errorf("hooks.log has no stop line: %v %q", err, log)
	}
}

func waitDown(t *testing.T, addr string) {
	t.Helper()
	for range 100 {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon still up after /shutdown")
}
