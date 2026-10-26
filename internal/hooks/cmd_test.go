package hooks_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rcrespodev/neugram/internal/client"
	"github.com/rcrespodev/neugram/internal/hooks"
)

type fake struct {
	mu    sync.Mutex
	calls []string
	reply map[string]string
}

func newFake(t *testing.T, reply map[string]string) (*fake, *client.Client) {
	t.Helper()
	f := &fake{reply: reply}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"v1"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		body, ok := f.reply[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(body, "ERR:") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"code":"` + body[4:] + `","message":"nope"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("NEUGRAM_HOME", home)
	prev := client.StartDaemon
	client.StartDaemon = func(string) error { return errors.New("start disabled") }
	t.Cleanup(func() { client.StartDaemon = prev })
	c := client.New("v1")
	c.Addr = strings.TrimPrefix(srv.URL, "http://")
	return f, c
}

func run(c *client.Client, event string, in any) (int, string, string) {
	raw, _ := json.Marshal(in)
	var out, errw bytes.Buffer
	code := hooks.Run(event, bytes.NewReader(raw), &out, &errw, c)
	return code, out.String(), errw.String()
}

func logText(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(os.Getenv("NEUGRAM_HOME"), "hooks.log"))
	return string(b)
}

func TestSessionStartPrintsRouteText(t *testing.T) {
	f, c := newFake(t, map[string]string{"/hooks/session-start": `{"stdout":"CLAUDE_CODE_SESSION_ID=s1\n\nPin: none\n"}`})
	code, out, _ := run(c, "session-start", map[string]string{"session_id": "s1", "cwd": "/x", "source": "startup"})
	if code != 0 || out != "CLAUDE_CODE_SESSION_ID=s1\n\nPin: none\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	if !strings.Contains(logText(t), "event=session-start session=s1") {
		t.Fatalf("log: %s", logText(t))
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestPromptIsSavedSilently(t *testing.T) {
	f, c := newFake(t, nil)
	code, out, _ := run(c, "user-prompt-submit", map[string]string{"session_id": "s1", "prompt": "hacé el refactor del módulo"})
	if code != 0 || out != "" || len(f.calls) != 1 || f.calls[0] != "POST /prompts" {
		t.Fatalf("code %d out %q calls %v", code, out, f.calls)
	}
}

func TestShortPromptIsNotSaved(t *testing.T) {
	f, c := newFake(t, nil)
	for _, p := range []string{"ok", "sí", "  1 2 3  ", "abcdefghi"} {
		code, out, _ := run(c, "user-prompt-submit", map[string]string{"session_id": "s1", "prompt": p})
		if code != 0 || out != "" {
			t.Fatalf("%q: code %d out %q", p, code, out)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestStopBlockPrintsDecision(t *testing.T) {
	reply := `{"decision":"block","reason":"**gate**\nfix it","gate":"g","gates_evaluated":["g"]}`
	_, c := newFake(t, map[string]string{"/hooks/stop": reply})
	code, out, _ := run(c, "stop", map[string]any{"session_id": "s1", "transcript_path": "/t.jsonl"})
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("code %d out %q err %v", code, out, err)
	}
	if len(got) != 2 || got["decision"] != "block" || got["reason"] != "**gate**\nfix it" {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(logText(t), "gates=g result=block:g") {
		t.Fatalf("log: %s", logText(t))
	}
}

func TestStopPassPrintsNothing(t *testing.T) {
	_, c := newFake(t, map[string]string{"/hooks/subagent-stop": `{"decision":null,"gate":null,"gates_evaluated":["a","b"]}`})
	code, out, _ := run(c, "subagent-stop", map[string]any{"session_id": "s1", "transcript_path": "/t.jsonl", "agent_transcript_path": "/a.jsonl", "agent_id": "x"})
	if code != 0 || out != "" {
		t.Fatalf("code %d out %q", code, out)
	}
	if !strings.Contains(logText(t), "gates=a,b result=pass") {
		t.Fatalf("log: %s", logText(t))
	}
}

func TestStopHookActiveSkipsDaemon(t *testing.T) {
	f, c := newFake(t, nil)
	for _, ev := range []string{"stop", "subagent-stop"} {
		code, out, _ := run(c, ev, map[string]any{"session_id": "s1", "transcript_path": "/t.jsonl", "stop_hook_active": true})
		if code != 0 || out != "" {
			t.Fatalf("%s: code %d out %q", ev, code, out)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestSessionEndPostsEnd(t *testing.T) {
	f, c := newFake(t, nil)
	code, out, _ := run(c, "session-end", map[string]string{"session_id": "s1", "reason": "other"})
	if code != 0 || out != "" || len(f.calls) != 1 || f.calls[0] != "POST /sessions/s1/end" {
		t.Fatalf("code %d out %q calls %v", code, out, f.calls)
	}
}

func TestUnreachableDaemonFailsOpen(t *testing.T) {
	_, c := newFake(t, nil)
	c.Addr = "127.0.0.1:1"
	for _, ev := range hooks.Events {
		code, out, errw := run(c, ev, map[string]any{"session_id": "s1", "cwd": "/x", "source": "startup", "prompt": "un prompt largo de verdad", "transcript_path": "/t.jsonl"})
		if code != 0 || out != "" || !strings.Contains(errw, "daemon_unreachable") {
			t.Fatalf("%s: code %d out %q err %q", ev, code, out, errw)
		}
	}
	if n := strings.Count(logText(t), "daemon_unreachable"); n != len(hooks.Events) {
		t.Fatalf("log has %d daemon_unreachable: %s", n, logText(t))
	}
}

func TestInvalidRequestFailsOpen(t *testing.T) {
	_, c := newFake(t, map[string]string{"/hooks/stop": "ERR:invalid_request"})
	code, out, errw := run(c, "stop", map[string]any{"session_id": "s1", "transcript_path": "/t.jsonl"})
	if code != 0 || out != "" || !strings.Contains(errw, "invalid_request") {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
	if !strings.Contains(logText(t), "result=fail_open") {
		t.Fatalf("log: %s", logText(t))
	}
}

func TestVersionMismatchNoticeGoesToLog(t *testing.T) {
	_, c := newFake(t, map[string]string{"/hooks/session-start": `{"stdout":"x"}`})
	t.Setenv("NEUGRAM_ADDR", "")
	c.Version = "other-wt-build"
	code, out, errw := run(c, "session-start", map[string]string{"session_id": "s1", "cwd": "/x", "source": "startup"})
	if code != 0 || out != "x" || errw != "" {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
	if !strings.Contains(logText(t), "no lo reemplazo") {
		t.Fatalf("log: %s", logText(t))
	}
}

func TestPanicFailsOpen(t *testing.T) {
	newFake(t, nil)
	var out, errw bytes.Buffer
	code := hooks.Run("session-start", strings.NewReader(`{"session_id":"s1"}`), &out, &errw, nil)
	if code != 0 || out.Len() != 0 || !strings.Contains(errw.String(), "panic") {
		t.Fatalf("code %d out %q err %q", code, out.String(), errw.String())
	}
	if !strings.Contains(logText(t), "reason=\"panic") {
		t.Fatalf("log: %s", logText(t))
	}
}

func TestSlowDaemonHonorsTotalBudget(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	t.Setenv("NEUGRAM_HOME", t.TempDir())
	var started sync.WaitGroup
	started.Add(1)
	prev := client.StartDaemon
	client.StartDaemon = func(string) error { defer started.Done(); <-release; return nil }
	t.Cleanup(func() { close(release); started.Wait(); slow.Close(); client.StartDaemon = prev })
	defer hooks.SetBudget(300 * time.Millisecond)()
	c := client.New("v1")
	c.Addr = strings.TrimPrefix(slow.URL, "http://")
	start := time.Now()
	code, out, errw := run(c, "stop", map[string]any{"session_id": "s1", "transcript_path": "/t.jsonl"})
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s with a 300ms budget", d)
	}
	if code != 0 || out != "" || !strings.Contains(errw, "daemon_unreachable") {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
}

func TestMissingSessionIDIsInvalidInput(t *testing.T) {
	f, c := newFake(t, nil)
	for _, ev := range hooks.Events {
		code, out, errw := run(c, ev, map[string]any{"prompt": "un prompt largo de verdad"})
		if code != 0 || out != "" || !strings.Contains(errw, "invalid_input") || strings.Contains(errw, "daemon_unreachable") {
			t.Fatalf("%s: code %d out %q err %q", ev, code, out, errw)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestMalformedStdinFailsOpen(t *testing.T) {
	_, c := newFake(t, nil)
	var out, errw bytes.Buffer
	code := hooks.Run("stop", strings.NewReader("not json"), &out, &errw, c)
	if code != 0 || out.Len() != 0 || !strings.Contains(errw.String(), "invalid_input") {
		t.Fatalf("code %d out %q err %q", code, out.String(), errw.String())
	}
}
