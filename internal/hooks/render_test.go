package hooks

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderSessionStartCapsHandoffAndOpenTasks(t *testing.T) {
	v := StartView{SessionID: "x", Instructions: "ins", OpenTotal: MaxStartTasks + 3}
	for range MaxStartTasks + 3 {
		v.OpenTasks = append(v.OpenTasks, StartTask{Key: "K", Title: "t", Status: "todo"})
	}
	v.Handoff = &StartHandoff{Title: "h", Content: strings.Repeat("a", maxHandoffRunes+5)}
	out := RenderSessionStart(v)
	if !strings.HasPrefix(out, "CLAUDE_CODE_SESSION_ID=x\n") {
		t.Fatalf("out = %q", out[:40])
	}
	if strings.Count(out, "- K — t [todo]") != MaxStartTasks || !strings.Contains(out, "- y 3 más") {
		t.Errorf("tareas mal acotadas:\n%s", out)
	}
	if !strings.Contains(out, "[handoff truncado") || strings.Contains(out, strings.Repeat("a", maxHandoffRunes+1)) {
		t.Error("handoff sin truncar")
	}
}

func TestRenderSessionStartTruncatesMultibyteOnRuneBoundary(t *testing.T) {
	out := RenderSessionStart(StartView{SessionID: "x", Handoff: &StartHandoff{Title: "h", Content: strings.Repeat("ñ", maxHandoffRunes+1)}})
	if !utf8.ValidString(out) || !strings.Contains(out, strings.Repeat("ñ", maxHandoffRunes)+"\n[handoff truncado") {
		t.Error("corte fuera de frontera de runa")
	}
}
