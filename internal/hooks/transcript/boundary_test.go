package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsBoundary(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"humano string", `{"type":"user","origin":{"kind":"human"},"message":{"content":"hola"}}`, true},
		{"humano sin origin", `{"type":"user","message":{"content":"hola"}}`, true},
		{"humano como lista de texto", `{"type":"user","message":{"content":[{"type":"text","text":"hola"}]}}`, true},
		{"humano vacio", `{"type":"user","message":{"content":"  "}}`, false},
		{"task-notification por origin", `{"type":"user","origin":{"kind":"task-notification"},"message":{"content":"x"}}`, true},
		{"task-notification por prefijo", `{"type":"user","message":{"content":"<task-notification>\n<task-id>a</task-id>"}}`, true},
		{"teammate-message", `{"type":"user","message":{"content":"<teammate-message teammate_id=\"a\">hola</teammate-message>"}}`, true},
		{"agent-message sin origin", `{"type":"user","message":{"content":"Another Claude session sent a message: <teammate-message>x"}}`, true},
		{"agent-message peer meta", `{"type":"user","isMeta":true,"origin":{"kind":"peer"},"message":{"content":"Another Claude session sent a message: x"}}`, true},
		{"agent-message meta por prefijo", `{"type":"user","isMeta":true,"message":{"content":"Another Claude session sent a message: x"}}`, true},
		{"interrupcion como texto", `{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`, true},
		{"isMeta no es borde", `{"type":"user","isMeta":true,"message":{"content":"nota del arnes"}}`, false},
		{"auto-continuation meta", `{"type":"user","isMeta":true,"origin":{"kind":"auto-continuation"},"message":{"content":"Your usage limit"}}`, false},
		{"stop hook feedback meta", `{"type":"user","isMeta":true,"message":{"content":"Stop hook feedback: x"}}`, false},
		{"local-command-stdout", `{"type":"user","message":{"content":"<local-command-stdout>x</local-command-stdout>"}}`, false},
		{"local-command-caveat", `{"type":"user","message":{"content":"<local-command-caveat>x"}}`, false},
		{"bash-stdout", `{"type":"user","message":{"content":"<bash-stdout>x</bash-stdout>"}}`, false},
		{"tool_result", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"x"}]}}`, false},
		{"assistant", `{"type":"assistant","message":{"content":[{"type":"text","text":"hola"}]}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, ok := parseLine([]byte(c.line))
			if !ok {
				t.Fatal("linea invalida")
			}
			if got := isBoundary(e); got != c.want {
				t.Fatalf("isBoundary = %v, quiero %v", got, c.want)
			}
		})
	}
}

func TestWakeUpEndsTheTurn(t *testing.T) {
	wakes := []string{
		`{"type":"user","origin":{"kind":"task-notification"},"message":{"content":"<task-notification>done"}}`,
		`{"type":"user","message":{"content":"<teammate-message>x"}}`,
		`{"type":"user","message":{"content":"Another Claude session sent a message: x"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	}
	for _, wake := range wakes {
		old := render([]step{user("a"), edit(src)})
		fresh := render([]step{call("Read", nil)})
		path := filepath.Join(t.TempDir(), "t.jsonl")
		writeFile(t, path, old+wake+"\n"+fresh)
		res, err := Scan(path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Mutated() {
			t.Errorf("la mutacion previa al despertar no pertenece al turno: %.60s", wake)
		}
	}
}

func TestUnansweredToolUseIsIgnored(t *testing.T) {
	line := `{"type":"assistant","cwd":"/home/r/proj","message":{"content":[{"type":"tool_use","id":"x","name":"Edit","input":{"file_path":"/home/r/proj/a.go"}}]}}`
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, line+"\n")
	res, err := Scan(path, Options{})
	if err != nil || res.Mutated() {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestInvalidLineInTheMiddle(t *testing.T) {
	good := render([]step{user("a"), edit(src)})
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, good+"{not json\n\n"+render([]step{edit(src + "2")}))
	res, err := Scan(path, Options{})
	if err != nil || len(res.Mutations) != 2 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestEveryChunkSize(t *testing.T) {
	defer func(n int) { chunkSize = n }(chunkSize)
	text := render([]step{user("a"), edit(src), user("ñandú 日本語 b"), edit(src + "2"), call("Read", nil), edit(src + "3")})
	for _, trailing := range []string{"", "\n"} {
		path := filepath.Join(t.TempDir(), "t.jsonl")
		writeFile(t, path, strings.TrimRight(text, "\n")+trailing)
		for size := 1; size <= 300; size++ {
			chunkSize = size
			res, err := Scan(path, Options{})
			if err != nil || len(res.Mutations) != 2 {
				t.Fatalf("chunk %d: %v %+v", size, err, res.Mutations)
			}
		}
	}
}
