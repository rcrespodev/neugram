package server

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

type startBody struct {
	Stdout         string `json:"stdout"`
	SessionCreated bool   `json:"session_created"`
	Pin            *struct {
		TaskKey string `json:"task_key"`
		Status  string `json:"status"`
	} `json:"pin"`
	WorktreesAlive int `json:"worktrees_alive"`
}

func (f *fixture) sessionStart(id, source string) startBody {
	f.t.Helper()
	var out startBody
	f.post("/hooks/session-start", map[string]string{"session_id": id, "cwd": "/repo", "source": source}, http.StatusOK, &out)
	return out
}

func requireContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("falta %q en:\n%s", w, got)
		}
	}
}

func TestSessionStartCreatesUnpinnedSession(t *testing.T) {
	f := newFixture(t)
	out := f.sessionStart("new-1", "startup")
	if !out.SessionCreated || out.Pin != nil {
		t.Fatalf("out = %+v", out)
	}
	if first, _, _ := strings.Cut(out.Stdout, "\n"); first != "CLAUDE_CODE_SESSION_ID=new-1" {
		t.Fatalf("primera línea = %q", first)
	}
	requireContains(t, out.Stdout, "## Tools de flujo", "Sin pin todavía", "EPIC-1 — Epic [in-progress] (Microservices)", "zeta — Lowercase [todo] (sin producto)", "pista, no es de tu tarea", "handoff body")
	if strings.Contains(out.Stdout, "DONE-1") || strings.Contains(out.Stdout, "Pin:") {
		t.Errorf("salida inesperada:\n%s", out.Stdout)
	}
}

func TestSessionStartKeepsPinAndRepeatsHandoff(t *testing.T) {
	f := newFixture(t)
	for _, source := range []string{"resume", "compact", "fork"} {
		out := f.sessionStart("s1", source)
		if out.SessionCreated || out.Pin == nil || out.Pin.TaskKey != "EPIC-1" {
			t.Fatalf("%s: out = %+v", source, out)
		}
		if first, _, _ := strings.Cut(out.Stdout, "\n"); first != "CLAUDE_CODE_SESSION_ID=s1" {
			t.Fatalf("%s: primera línea = %q", source, first)
		}
		requireContains(t, out.Stdout, "Pin: EPIC-1 — Epic [in-progress]", "## Último handoff\nHandoff EPIC-1", "handoff body")
		if strings.Contains(out.Stdout, "pista") || strings.Contains(out.Stdout, "Tareas abiertas") {
			t.Errorf("%s: no debía traer pista ni tareas:\n%s", source, out.Stdout)
		}
	}
}

func TestSessionStartForkNewIDLeadsWithIt(t *testing.T) {
	f := newFixture(t)
	out := f.sessionStart("forked-1", "fork")
	if first, _, _ := strings.Cut(out.Stdout, "\n"); first != "CLAUDE_CODE_SESSION_ID=forked-1" {
		t.Fatalf("primera línea = %q", first)
	}
}

func TestSessionStartIsIdempotent(t *testing.T) {
	f := newFixture(t)
	first := f.sessionStart("new-2", "startup")
	second := f.sessionStart("new-2", "startup")
	if first.Stdout != second.Stdout || !first.SessionCreated || second.SessionCreated {
		t.Fatalf("first = %+v\nsecond = %+v", first, second)
	}
	db, err := sql.Open("sqlite", f.srv.opts.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'new-2'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("filas = %d, err = %v", n, err)
	}
}

func TestSessionStartClearHintsLastPinOfCwd(t *testing.T) {
	f := newFixture(t)
	out := f.sessionStart("after-clear", "clear")
	if out.Pin != nil {
		t.Fatalf("pin = %+v", out.Pin)
	}
	requireContains(t, out.Stdout, "CLAUDE_CODE_SESSION_ID=after-clear", "Sin pin todavía", "tenía pin EPIC-1; no se hereda", "pista, no es de tu tarea", "handoff body")
	startup := f.sessionStart("plain", "startup")
	if strings.Contains(startup.Stdout, "no se hereda") {
		t.Error("startup no lleva la pista de pin")
	}
}

func TestSessionStartMarksDeadWorktree(t *testing.T) {
	f := newFixture(t)
	out := f.sessionStart("s1", "resume")
	requireContains(t, out.Stdout, f.alivePath+" (feat/a) [vivo]", f.deadPath+" (feat/b) [muerto]")
	if out.WorktreesAlive != 1 {
		t.Fatalf("worktrees_alive = %d", out.WorktreesAlive)
	}
}

func TestSessionStartPinnedNone(t *testing.T) {
	f := newFixture(t)
	f.sessionStart("n1", "startup")
	f.post("/sessions/n1/pin", map[string]string{"task_id": "none"}, http.StatusOK, nil)
	out := f.sessionStart("n1", "resume")
	requireContains(t, out.Stdout, "Pin: none")
	if strings.Contains(out.Stdout, "Tareas abiertas") || strings.Contains(out.Stdout, "handoff body") {
		t.Errorf("salida inesperada:\n%s", out.Stdout)
	}
}

func TestSessionStartRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	for _, body := range []map[string]string{
		{"cwd": "/repo", "source": "startup"},
		{"session_id": "x", "cwd": "rel", "source": "startup"},
		{"session_id": "x", "cwd": "/repo", "source": "nope"},
	} {
		f.post("/hooks/session-start", body, http.StatusBadRequest, nil)
	}
}
