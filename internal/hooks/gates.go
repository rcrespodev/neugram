package hooks

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rcrespodev/neugram/internal/hooks/transcript"
)

const (
	GatePinOnMutation      = "require-task-pin-on-mutation"
	GateSaveOnMutation     = "require-save-on-mutation"
	GateWorktreeRegistered = "require-worktree-registered"

	toolWorktreeRegister = "mcp__neugram__worktree_register"
	maxFilesInReason     = 10
	maxTasksInReason     = 10
	maxQuotedRunes       = 200
	defaultMinFiles      = 1
)

// TurnContext is what a gate sees: the scanned turn plus the session state.
type TurnContext struct {
	SessionID string
	Pinned    bool
	// OpenTasks are "KEY — title [status]" lines for the unblock hint.
	OpenTasks []string
	Subagent  bool
	Turn      *transcript.Result
	Params    json.RawMessage
}

type Violation struct {
	Gate   string
	Reason string
}

// Gate is one entry of the fixed set in docs/specs/instructions-and-gates.md.
type Gate struct {
	Name           string
	DefaultEnabled bool
	// OnSubagentStop marks the gates evaluated on a subagent transcript.
	OnSubagentStop bool
	Check          func(TurnContext) *Violation
	// Phrase says what the gate blocks, for GET /instructions.
	Phrase string
}

// Override is a row of the gates table; it only replaces enabled and params.
type Override struct {
	Enabled bool
	Params  json.RawMessage
}

// Gates is the fixed set, in evaluation order.
var Gates = []Gate{
	{GatePinOnMutation, true, true, checkPin,
		"el turno no cierra si editó archivos y la sesión no tiene pin (task_pin; none cuenta)"},
	{GateSaveOnMutation, true, false, checkSave,
		"el turno no cierra si editó archivos y no hubo mem_save, mem_update ni handoff_save"},
	{GateWorktreeRegistered, true, true, checkWorktree,
		"el turno no cierra si creó un worktree sin worktree_register de ese path"},
}

// EnabledGates is the set Evaluate runs on a main-agent Stop, with the
// overrides applied.
func EnabledGates(overrides map[string]Override) []Gate {
	var out []Gate
	for _, g := range Gates {
		enabled := g.DefaultEnabled
		if o, ok := overrides[g.Name]; ok {
			enabled = o.Enabled
		}
		if enabled {
			out = append(out, g)
		}
	}
	return out
}

type Decision struct {
	Violation *Violation
	Evaluated []string
}

// Evaluate runs the enabled gates in order and stops at the first violation.
func Evaluate(ctx TurnContext, overrides map[string]Override) Decision {
	d := Decision{Evaluated: []string{}}
	for _, g := range Gates {
		if ctx.Subagent && !g.OnSubagentStop {
			continue
		}
		enabled, params := g.DefaultEnabled, json.RawMessage(nil)
		if o, ok := overrides[g.Name]; ok {
			enabled, params = o.Enabled, o.Params
		}
		if !enabled {
			continue
		}
		d.Evaluated = append(d.Evaluated, g.Name)
		ctx.Params = params
		if v := g.Check(ctx); v != nil {
			d.Violation = v
			return d
		}
	}
	return d
}

func MutatedFiles(r *transcript.Result) []string {
	files := []string{}
	for _, m := range r.Mutations {
		p := m.Path
		if p == "" {
			p = m.Command
		}
		if p != "" && !slices.Contains(files, p) {
			files = append(files, p)
		}
	}
	return files
}

// SaveCount counts the memory writes; a worktree_register is not one.
func SaveCount(r *transcript.Result) int {
	n := 0
	for _, c := range r.Saves {
		if c.Tool != toolWorktreeRegister {
			n++
		}
	}
	return n
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxQuotedRunes {
		return s
	}
	return string(r[:maxQuotedRunes]) + "…"
}

func filesLine(files []string) string {
	shown := make([]string, 0, maxFilesInReason)
	for _, f := range files[:min(len(files), maxFilesInReason)] {
		shown = append(shown, truncate(f))
	}
	line := "`" + strings.Join(shown, "`, `") + "`"
	if len(files) > len(shown) {
		line += fmt.Sprintf(" y %d más", len(files)-len(shown))
	}
	return line
}

func checkPin(c TurnContext) *Violation {
	if !c.Turn.Mutated() || c.Pinned {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n\nEl turno tocó %s y la sesión no tiene pin.\n\n", GatePinOnMutation, filesLine(MutatedFiles(c.Turn)))
	if len(c.OpenTasks) > 0 {
		b.WriteString("Tareas abiertas:\n")
		for _, t := range c.OpenTasks[:min(len(c.OpenTasks), maxTasksInReason)] {
			b.WriteString("- " + truncate(t) + "\n")
		}
		if extra := len(c.OpenTasks) - maxTasksInReason; extra > 0 {
			fmt.Fprintf(&b, "- y %d más (`task_list`)\n", extra)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Destrabar: `task_pin {\"session_id\": %q, \"task_id\": \"<key>\"}` o `task_pin {\"session_id\": %q, \"task_id\": \"none\"}`.", c.SessionID, c.SessionID)
	return &Violation{GatePinOnMutation, b.String()}
}

func checkSave(c TurnContext) *Violation {
	files := MutatedFiles(c.Turn)
	minFiles := defaultMinFiles
	var p struct {
		MinFiles *int `json:"min_files"`
	}
	if json.Unmarshal(c.Params, &p) == nil && p.MinFiles != nil {
		minFiles = *p.MinFiles
	}
	if len(files) == 0 || len(files) < minFiles || SaveCount(c.Turn) > 0 {
		return nil
	}
	return &Violation{GateSaveOnMutation, fmt.Sprintf(
		"**%s**\n\nEl turno tocó %s y no guardó nada en memoria.\n\nDestrabar: `mem_save` (o `mem_update` / `handoff_save`) con `session_id` %q y la línea `**Task**` al final del content.",
		GateSaveOnMutation, filesLine(files), c.SessionID)}
}

func checkWorktree(c TurnContext) *Violation {
	var registered []string
	for _, call := range c.Turn.Saves {
		if call.Tool != toolWorktreeRegister {
			continue
		}
		if p, ok := call.Input["path"].(string); ok && p != "" {
			registered = append(registered, filepath.Clean(p))
		}
	}
	var missing []string
	for _, w := range c.Turn.Worktrees {
		if w.Unresolved || w.Path == "" {
			continue
		}
		if !slices.Contains(registered, filepath.Clean(w.Path)) {
			missing = append(missing, w.Path)
		}
	}
	if len(missing) > 0 {
		return &Violation{GateWorktreeRegistered, fmt.Sprintf(
			"**%s**\n\nEl turno creó el worktree %s sin registrarlo.\n\nDestrabar: `worktree_register {\"session_id\": %q, \"path\": %q, \"repo\": \"<repo>\", \"branch\": \"<rama>\"}`.",
			GateWorktreeRegistered, filesLine(missing), c.SessionID, missing[0])}
	}
	if len(registered) > 0 {
		return nil
	}
	var cmds []string
	for _, m := range c.Turn.Mutations {
		if m.Worktree && m.Unresolved {
			cmds = append(cmds, truncate(m.Command))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return &Violation{GateWorktreeRegistered, fmt.Sprintf(
		"**%s**\n\nEl turno creó un worktree cuyo path no se pudo expandir: `%s`.\n\nRegistrá el path absoluto: `worktree_register {\"session_id\": %q, \"path\": \"<path absoluto>\", \"repo\": \"<repo>\", \"branch\": \"<rama>\"}`.",
		GateWorktreeRegistered, strings.Join(cmds, "`, `"), c.SessionID)}
}
