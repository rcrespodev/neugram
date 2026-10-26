package hooks

import (
	"fmt"
	"strings"
)

const (
	maxHandoffRunes = 12000
	maxStartTasks   = maxTasksInReason
)

type StartTask struct {
	Key, Title, Status, Product string
}

type StartWorktree struct {
	Path, Branch string
	Alive        bool
}

type StartHandoff struct {
	Title, Content string
}

// StartView is what session-start prints. PinKey "none" is a pin to no task.
type StartView struct {
	SessionID     string
	Instructions  string
	Pinned        bool
	PinKey        string
	PinTitle      string
	PinStatus     string
	OpenTasks     []StartTask
	OpenTotal     int
	LastPin       string
	Handoff       *StartHandoff
	HandoffIsHint bool
	Worktrees     []StartWorktree
}

// RenderSessionStart builds the stdout of the session-start hook. The first
// line is always CLAUDE_CODE_SESSION_ID.
func RenderSessionStart(v StartView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CLAUDE_CODE_SESSION_ID=%s\n\n", v.SessionID)
	b.WriteString(strings.TrimRight(v.Instructions, "\n") + "\n\n")

	switch {
	case v.Pinned && v.PinKey == "none":
		b.WriteString("Pin: none\n")
	case v.Pinned:
		fmt.Fprintf(&b, "Pin: %s — %s [%s]\n", v.PinKey, v.PinTitle, v.PinStatus)
	default:
		b.WriteString("Sin pin todavía. Fijalo con task_pin antes de tocar archivos.\n")
		if v.LastPin != "" {
			fmt.Fprintf(&b, "Pista: la última sesión pineada de este directorio tenía pin %s; no se hereda.\n", v.LastPin)
		}
		if len(v.OpenTasks) > 0 {
			b.WriteString("\nTareas abiertas:\n")
			for _, t := range v.OpenTasks[:min(len(v.OpenTasks), maxStartTasks)] {
				product := t.Product
				if product == "" {
					product = "sin producto"
				}
				b.WriteString("- " + truncate(fmt.Sprintf("%s — %s [%s] (%s)", t.Key, t.Title, t.Status, product)) + "\n")
			}
			if extra := v.OpenTotal - maxStartTasks; extra > 0 {
				fmt.Fprintf(&b, "- y %d más (`task_list`)\n", extra)
			}
		}
	}

	if v.Handoff != nil {
		heading := "## Último handoff"
		if v.HandoffIsHint {
			heading = "## Último handoff del directorio (pista, no es de tu tarea)"
		}
		fmt.Fprintf(&b, "\n%s\n%s\n\n%s\n", heading, v.Handoff.Title, capRunes(v.Handoff.Content, maxHandoffRunes))
	}

	if len(v.Worktrees) > 0 {
		b.WriteString("\n## Worktrees de la tarea\n")
		for _, w := range v.Worktrees {
			state := "muerto"
			if w.Alive {
				state = "vivo"
			}
			fmt.Fprintf(&b, "- %s (%s) [%s]\n", w.Path, w.Branch, state)
		}
	}
	return b.String()
}

func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n[handoff truncado: task_show trae el resto]"
}
