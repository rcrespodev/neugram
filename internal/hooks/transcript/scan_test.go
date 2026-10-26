package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	src     = "/home/r/proj/src/app.ts"
	scratch = "/tmp/claude-1000/x/scratchpad/notes.md"
	cwd     = "/home/r/proj"
)

type step struct {
	tool  string
	input map[string]any
	fail  bool
	human string
	cwd   string
}

func user(text string) step { return step{human: text} }

func call(tool string, input map[string]any) step { return step{tool: tool, input: input} }

func failed(s step) step { s.fail = true; return s }

func inCwd(s step, dir string) step { s.cwd = dir; return s }

func bash(command string) step { return call("Bash", map[string]any{"command": command}) }

func render(steps []step) string {
	var b strings.Builder
	emit := func(v any) {
		line, _ := json.Marshal(v)
		b.Write(line)
		b.WriteByte('\n')
	}
	for i, s := range steps {
		dir := s.cwd
		if dir == "" {
			dir = cwd
		}
		if s.human != "" {
			emit(map[string]any{"type": "user", "cwd": dir, "message": map[string]any{"role": "user", "content": s.human}})
			continue
		}
		id := "toolu_" + string(rune('a'+i))
		emit(map[string]any{"type": "assistant", "cwd": dir, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": s.tool, "input": s.input},
		}}})
		result := map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"}
		if s.fail {
			result["is_error"] = true
		}
		emit(map[string]any{"type": "user", "cwd": dir, "message": map[string]any{"role": "user", "content": []any{result}}})
	}
	return b.String()
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func scanSteps(t *testing.T, mode Mode, steps ...step) *Result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(render(steps)), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Scan(path, Options{Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func edit(path string) step { return call("Edit", map[string]any{"file_path": path}) }

func TestTurnBoundary(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		steps []step
		want  int
	}{
		{"edit sin mensaje humano", Turn, []step{edit(src)}, 1},
		{"edit antes del ultimo mensaje humano", Turn, []step{edit(src), user("y ahora esto"), call("Read", nil)}, 0},
		{"edit despues del ultimo mensaje humano", Turn, []step{edit(src), user("y ahora esto"), edit(src)}, 1},
		{"solo cuenta el ultimo turno", Turn, []step{user("a"), edit(src), user("b"), edit(src), edit(src + "2")}, 2},
		{"entero ignora los mensajes humanos", Entire, []step{edit(src), user("b"), edit(src + "2")}, 2},
		{"tool_result no corta el turno", Turn, []step{user("a"), edit(src), call("Read", nil), edit(src + "2")}, 2},
		{"solo lecturas", Turn, []step{user("mostrame"), call("Read", map[string]any{"file_path": src}), call("Grep", nil)}, 0},
		{"edit fallido no cuenta", Turn, []step{failed(edit(src))}, 0},
		{"scratchpad no cuenta", Turn, []step{edit(scratch)}, 0},
		{"scratchpad mas fuente", Turn, []step{edit(scratch), edit(src)}, 1},
		{"Write MultiEdit NotebookEdit", Turn, []step{
			call("Write", map[string]any{"file_path": src}),
			call("MultiEdit", map[string]any{"file_path": src + "2"}),
			call("NotebookEdit", map[string]any{"notebook_path": "/home/r/proj/n.ipynb"}),
		}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := scanSteps(t, c.mode, c.steps...)
			if len(res.Mutations) != c.want {
				t.Fatalf("mutaciones = %d, quiero %d: %+v", len(res.Mutations), c.want, res.Mutations)
			}
		})
	}
}

func TestScanFixtures(t *testing.T) {
	cases := []struct {
		file string
		mode Mode
		want []string
	}{
		{"turn-after-human.jsonl", Turn, []string{"/home/r/proj/src/b.go"}},
		{"turn-after-human.jsonl", Entire, []string{"/home/r/proj/src/a.go", "/home/r/proj/src/b.go"}},
		{"noise.jsonl", Turn, []string{"/home/r/proj/src/c.go"}},
		{"wake.jsonl", Turn, []string{"/home/r/proj/src/after.go"}},
		{"wake.jsonl", Entire, []string{"/home/r/proj/src/before.go", "/home/r/proj/src/after.go"}},
	}
	for _, c := range cases {
		res, err := Scan(filepath.Join("testdata", c.file), Options{Mode: c.mode})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range res.Mutations {
			got = append(got, m.Path)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s mode %d: %v, quiero %v", c.file, c.mode, got, c.want)
		}
	}
}

func TestMissingFile(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "nope.jsonl"), Options{}); err == nil {
		t.Fatal("quiero error")
	}
}

func TestTurnAcrossChunks(t *testing.T) {
	big := strings.Repeat("x", 3*chunkSize)
	steps := []step{user("a"), edit(src), call("Read", map[string]any{"file_path": big}), user("b"), edit(src + "2")}
	res := scanSteps(t, Turn, steps...)
	if len(res.Mutations) != 1 || res.Mutations[0].Path != src+"2" {
		t.Fatalf("%+v", res.Mutations)
	}
	res = scanSteps(t, Turn, user("a"), call("Read", map[string]any{"file_path": big}), edit(src))
	if len(res.Mutations) != 1 {
		t.Fatalf("%+v", res.Mutations)
	}
}

func TestTruncatedLastLineIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	data := render([]step{user("a"), edit(src)}) + `{"type":"assistant","mess`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Scan(path, Options{})
	if err != nil || len(res.Mutations) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}
