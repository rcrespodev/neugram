package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rcrespodev/neugram/internal/hooks"
)

const hookBudgetWarm = 200 * time.Millisecond

type hookCase struct {
	name string
	in   map[string]any
}

func hookCases(t testing.TB) []hookCase {
	t.Helper()
	tr := filepath.Join(t.TempDir(), "t.jsonl")
	lines := `{"type":"user","cwd":"/repo","message":{"role":"user","content":"hola"}}` + "\n" +
		`{"type":"assistant","cwd":"/repo","message":{"role":"assistant","content":[{"type":"text","text":"listo"}]}}` + "\n"
	if err := os.WriteFile(tr, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return []hookCase{
		{"session-start", map[string]any{"session_id": "s1", "cwd": "/repo", "source": "startup"}},
		{"user-prompt-submit", map[string]any{"session_id": "s1", "prompt": "un prompt de largo razonable"}},
		{"stop", map[string]any{"session_id": "s1", "transcript_path": tr}},
		{"subagent-stop", map[string]any{"session_id": "s1", "transcript_path": tr, "agent_transcript_path": tr, "agent_id": "a"}},
		{"session-end", map[string]any{"session_id": "s1", "reason": "other"}},
	}
}

func runHookOnce(t testing.TB, hc hookCase, raw []byte) time.Duration {
	var out, errw bytes.Buffer
	start := time.Now()
	code := hooks.Run(hc.name, bytes.NewReader(raw), &out, &errw, newClient())
	d := time.Since(start)
	if code != 0 || errw.Len() != 0 {
		t.Fatalf("%s: code %d stderr %q", hc.name, code, errw.String())
	}
	return d
}

func TestHookLatencyHotDaemon(t *testing.T) {
	startServer(t, 0)
	t.Setenv("NEUGRAM_HOME", t.TempDir())
	for _, hc := range hookCases(t) {
		raw, _ := json.Marshal(hc.in)
		var runs []time.Duration
		for range 21 {
			runs = append(runs, runHookOnce(t, hc, raw))
		}
		slices.Sort(runs)
		t.Logf("%s: median of 21 = %s, max %s", hc.name, runs[10], runs[20])
		if runs[10] > hookBudgetWarm {
			t.Errorf("%s median %s over the budget %s", hc.name, runs[10], hookBudgetWarm)
		}
	}
}

func BenchmarkHookHotDaemon(b *testing.B) {
	startServer(b, 0)
	b.Setenv("NEUGRAM_HOME", b.TempDir())
	for _, hc := range hookCases(b) {
		raw, _ := json.Marshal(hc.in)
		b.Run(hc.name, func(b *testing.B) {
			for range b.N {
				runHookOnce(b, hc, raw)
			}
		})
	}
}
