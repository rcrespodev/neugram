package hooks

import (
	"strings"
	"testing"
)

func TestRenderSessionStartCapsHandoffAndOpenTasks(t *testing.T) {
	v := StartView{SessionID: "x", Instructions: "ins", OpenTotal: maxStartTasks + 3}
	for range maxStartTasks + 3 {
		v.OpenTasks = append(v.OpenTasks, StartTask{Key: "K", Title: "t", Status: "todo"})
	}
	v.Handoff = &StartHandoff{Title: "h", Content: strings.Repeat("a", maxHandoffRunes+5)}
	out := RenderSessionStart(v)
	if !strings.HasPrefix(out, "CLAUDE_CODE_SESSION_ID=x\n") {
		t.Fatalf("out = %q", out[:40])
	}
	if strings.Count(out, "- K — t [todo]") != maxStartTasks || !strings.Contains(out, "- y 3 más") {
		t.Errorf("tareas mal acotadas:\n%s", out)
	}
	if !strings.Contains(out, "[handoff truncado") || strings.Contains(out, strings.Repeat("a", maxHandoffRunes+1)) {
		t.Error("handoff sin truncar")
	}
}
