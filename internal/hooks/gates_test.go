package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rcrespodev/neugram/internal/hooks/transcript"
)

func edited() *transcript.Result {
	return &transcript.Result{Mutations: []transcript.Mutation{{Tool: "Edit", Path: "/p/a.go"}}}
}

func register(path string) transcript.Call {
	return transcript.Call{Tool: toolWorktreeRegister, Input: map[string]any{"path": path}}
}

func TestPinGateListsOpenTasksAndExactCall(t *testing.T) {
	d := Evaluate(TurnContext{SessionID: "s-1", OpenTasks: []string{"A-1 — Uno [todo]"}, Turn: edited()}, nil)
	if d.Violation == nil || d.Violation.Gate != GatePinOnMutation {
		t.Fatalf("decision = %+v", d)
	}
	for _, want := range []string{"A-1 — Uno [todo]", `task_pin {"session_id": "s-1", "task_id": "none"}`, "`/p/a.go`"} {
		if !strings.Contains(d.Violation.Reason, want) {
			t.Errorf("reason sin %q:\n%s", want, d.Violation.Reason)
		}
	}
}

func TestPinnedMutationWithSavePasses(t *testing.T) {
	turn := edited()
	turn.Saves = []transcript.Call{{Tool: "mcp__neugram__mem_save"}}
	d := Evaluate(TurnContext{Pinned: true, Turn: turn}, nil)
	if d.Violation != nil || len(d.Evaluated) != 3 {
		t.Fatalf("decision = %+v", d)
	}
}

func TestMutationWithoutSaveBlocksAndRegisterIsNotASave(t *testing.T) {
	turn := edited()
	turn.Saves = []transcript.Call{register("/w")}
	d := Evaluate(TurnContext{Pinned: true, Turn: turn}, nil)
	if d.Violation == nil || d.Violation.Gate != GateSaveOnMutation {
		t.Fatalf("decision = %+v", d)
	}
}

func TestSubagentStopSkipsSaveGate(t *testing.T) {
	d := Evaluate(TurnContext{Pinned: true, Subagent: true, Turn: edited()}, nil)
	if d.Violation != nil || strings.Join(d.Evaluated, ",") != GatePinOnMutation+","+GateWorktreeRegistered {
		t.Fatalf("decision = %+v", d)
	}
}

func TestWorktreeGate(t *testing.T) {
	wt := func(path string, unresolved bool) *transcript.Result {
		return &transcript.Result{
			Mutations: []transcript.Mutation{{Tool: "Bash", Path: path, Command: "git worktree add " + path, Worktree: true, Unresolved: unresolved}},
			Worktrees: []transcript.Worktree{{Tool: "Bash", Path: path, Unresolved: unresolved}},
		}
	}
	cases := []struct {
		name  string
		turn  *transcript.Result
		saves []transcript.Call
		block bool
	}{
		{"sin register", wt("/w/a", false), nil, true},
		{"register del mismo path", wt("/w/a", false), []transcript.Call{register("/w/a/")}, false},
		{"register de otro path", wt("/w/a", false), []transcript.Call{register("/w/b")}, true},
		{"no expandible sin register", wt("$D/a", true), nil, true},
		{"no expandible con algun register", wt("$D/a", true), []transcript.Call{register("/w/a")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.turn.Saves = append(c.turn.Saves, transcript.Call{Tool: "mcp__neugram__mem_save"})
			c.turn.Saves = append(c.turn.Saves, c.saves...)
			d := Evaluate(TurnContext{Pinned: true, Turn: c.turn}, nil)
			if c.block != (d.Violation != nil && d.Violation.Gate == GateWorktreeRegistered) {
				t.Fatalf("decision = %+v", d)
			}
			if c.block && c.turn.Worktrees[0].Unresolved && !strings.Contains(d.Violation.Reason, "git worktree add $D/a") {
				t.Fatalf("el reason no cita el comando: %s", d.Violation.Reason)
			}
		})
	}
}

func TestOverrideDisablesGateAndParamsReachIt(t *testing.T) {
	off := map[string]Override{GatePinOnMutation: {Enabled: false}}
	if d := Evaluate(TurnContext{Pinned: false, Turn: edited()}, off); d.Violation == nil || d.Violation.Gate != GateSaveOnMutation {
		t.Fatalf("con el pin apagado debe seguir el siguiente: %+v", d)
	}
	min := map[string]Override{GateSaveOnMutation: {Enabled: true, Params: json.RawMessage(`{"min_files": 2}`)}}
	if d := Evaluate(TurnContext{Pinned: true, Turn: edited()}, min); d.Violation != nil {
		t.Fatalf("min_files=2 con un archivo no bloquea: %+v", d)
	}
}

func TestReasonIsBounded(t *testing.T) {
	var open []string
	for i := 0; i < 25; i++ {
		open = append(open, "K-xxx — tarea [todo]")
	}
	long := strings.Repeat("a", 5000)
	turn := &transcript.Result{
		Mutations: []transcript.Mutation{{Tool: "Bash", Path: "$D/a", Command: "git worktree add " + long, Worktree: true, Unresolved: true}},
		Worktrees: []transcript.Worktree{{Tool: "Bash", Unresolved: true}},
	}
	d := Evaluate(TurnContext{OpenTasks: open, Turn: turn}, nil)
	if got := strings.Count(d.Violation.Reason, "- K-"); got != maxTasksInReason || !strings.Contains(d.Violation.Reason, "y 15 más") {
		t.Fatalf("tareas listadas = %d:\n%s", got, d.Violation.Reason)
	}
	turn.Saves = []transcript.Call{{Tool: "mcp__neugram__mem_save"}}
	d = Evaluate(TurnContext{Pinned: true, Turn: turn}, nil)
	if d.Violation == nil || len(d.Violation.Reason) > 1000 || !strings.Contains(d.Violation.Reason, "…") {
		t.Fatalf("el comando no se truncó: %+v", d)
	}
}
