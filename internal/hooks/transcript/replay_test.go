package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const legacyHelper = `
import importlib.machinery, importlib.util, json, sys
sys.dont_write_bytecode = True
loader = importlib.machinery.SourceFileLoader("gate", sys.argv[1])
spec = importlib.util.spec_from_loader("gate", loader)
gate = importlib.util.module_from_spec(spec)
loader.exec_module(gate)
for path in sys.argv[2:]:
    turn = gate.scan(path)
    print(json.dumps({"path": path, "wrote": turn.wrote, "saved": turn.saved,
                      "worktrees": len(turn.worktrees_pending)}))
`

type legacyVerdict struct {
	Path      string `json:"path"`
	Wrote     bool   `json:"wrote"`
	Saved     bool   `json:"saved"`
	Worktrees int    `json:"worktrees"`
}

func replayFiles(t *testing.T, spec string) []string {
	t.Helper()
	var files []string
	if info, err := os.Stat(spec); err == nil && info.IsDir() {
		_ = filepath.WalkDir(spec, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") && !strings.Contains(p, "/subagents/") {
				files = append(files, p)
			}
			return nil
		})
	} else {
		files, _ = filepath.Glob(spec)
	}
	sort.Strings(files)
	limit := 200
	if v, err := strconv.Atoi(os.Getenv("NEUGRAM_REPLAY_LIMIT")); err == nil && v > 0 {
		limit = v
	}
	if len(files) > limit {
		step := len(files) / limit
		var sample []string
		for i := 0; i < len(files) && len(sample) < limit; i += step {
			sample = append(sample, files[i])
		}
		files = sample
	}
	return files
}

func TestReplayAgainstLegacyGate(t *testing.T) {
	spec := os.Getenv("NEUGRAM_REPLAY_TRANSCRIPTS")
	if spec == "" {
		t.Skip("NEUGRAM_REPLAY_TRANSCRIPTS no esta seteado")
	}
	home, err := osuser.Current()
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(home.HomeDir, ".claude", "hooks", "engram-save-gate")
	if _, err := os.Stat(gate); err != nil {
		t.Skip("no existe el gate legacy")
	}
	files := replayFiles(t, spec)
	if len(files) == 0 {
		t.Skip("sin transcripts")
	}

	args := append([]string{"-I", "-c", legacyHelper, gate}, files...)
	out, err := exec.Command("python3", args...).Output()
	if err != nil {
		t.Fatalf("gate legacy: %v", err)
	}
	legacy := map[string]legacyVerdict{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var v legacyVerdict
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			legacy[v.Path] = v
		}
	}

	opts := Options{SaveTools: append([]string{
		"mcp__plugin_engram_engram__mem_save",
		"mcp__plugin_engram_engram__mem_update",
		"mcp__plugin_engram_engram__mem_session_summary",
	}, DefaultSaveTools...)}

	diffs := map[string]int{}
	var largest string
	var largestSize int64
	for _, f := range files {
		res, err := Scan(f, opts)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		want := legacy[f]
		wrote := false
		for _, m := range res.Mutations {
			if !m.Worktree {
				wrote = true
			}
		}
		check := func(kind string, got, legacyGot bool) {
			if got != legacyGot {
				diffs[kind]++
				t.Logf("DIFF %s go=%v legacy=%v %s\n  mutations=%.400v\n  saves=%d", kind, got, legacyGot, f, res.Mutations, len(res.Saves))
			}
		}
		check("mutated", wrote, want.Wrote)
		check("saved", len(res.Saves) > 0, want.Saved)
		check("worktree", len(res.Worktrees) > 0, want.Worktrees > 0)
		if info, err := os.Stat(f); err == nil && info.Size() > largestSize {
			largest, largestSize = f, info.Size()
		}
	}
	t.Logf("REPLAY transcripts=%d diffs=%v", len(files), diffs)

	start := time.Now()
	if _, err := Scan(largest, opts); err != nil {
		t.Fatal(err)
	}
	turn := time.Since(start)
	start = time.Now()
	if _, err := Scan(largest, Options{Mode: Entire, SaveTools: opts.SaveTools}); err != nil {
		t.Fatal(err)
	}
	t.Logf("REPLAY largest=%s size=%d turn=%s entire=%s", largest, largestSize, turn, time.Since(start))

	if os.Getenv("NEUGRAM_REPLAY_STRICT") != "" && len(diffs) > 0 {
		t.Fail()
	}
}
