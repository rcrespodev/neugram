package legacy

import (
	"testing"
	"time"
)

func TestParseFooter(t *testing.T) {
	cases := []struct {
		line string
		want footer
		ok   bool
	}{
		{"**Task**: TRIPULSE-3652 — Best Skills", footer{key: "TRIPULSE-3652", title: "Best Skills"}, true},
		{"**Task**: 869abc · TRIPULSE-3652 — Best Skills", footer{key: "TRIPULSE-3652", title: "Best Skills"}, true},
		{"**Task**: ninguna — setup de hooks", footer{none: true, title: "setup de hooks"}, true},
		{"**Task**: none", footer{none: true}, true},
		{"**Task**: pendiente de decidir", footer{}, true},
		{"  **Task**: engram-fork-debat — Fork — debate  ", footer{key: "engram-fork-debat", title: "Fork — debate"}, true},
		{"Task: TRIPULSE-1", footer{}, false},
	}
	for _, c := range cases {
		got, ok := parseFooter(c.line)
		if got != c.want || ok != c.ok {
			t.Errorf("parseFooter(%q) = %+v, %v; want %+v, %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestLastFooterIgnoresTrailingBlankLines(t *testing.T) {
	f, ok := lastFooter("body\n\n**Task**: KEY-1 — t\n\n  \n")
	if !ok || f.key != "KEY-1" {
		t.Errorf("got %+v, %v", f, ok)
	}
	if _, ok := lastFooter("**Task**: KEY-1 — t\n\nmore text"); ok {
		t.Error("footer not on the last line was accepted")
	}
}

func TestCanonicalProduct(t *testing.T) {
	cases := map[string]string{
		"Microservices (react) · parent TRIPULSE-3652":           "tripulse",
		"Portal / Administración Clientes (padre TRIPULSE-3242)": "tripulse",
		"Portal B2B - Vicsa Chile":                               "tripulse",
		"Soporte Chile / Integración SAP-Magento":                "tripulse",
		"TRIPULSE-3945 Socios de negocio":                        "tripulse",
		"dev-loop":                                               "tripulse",
		"engram-fork":                                            "engram-fork",
		"Engram-Fork (debate)":                                   "engram-fork",
		"ai-review":                                              "ai-review",
	}
	for in, want := range cases {
		if got := canonicalProduct(in); got != want {
			t.Errorf("canonicalProduct(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentityNotesMarkProductAsProvenance(t *testing.T) {
	id := parseIdentity("**Task**: X-1 — t\n**Product**: Microservices (react)\n\nContexto libre.")
	if want := "[import-legacy] **Product** original: Microservices (react)\n\nContexto libre."; id.notes != want {
		t.Errorf("notes = %q, want %q", id.notes, want)
	}
}

func TestParseWorktreeLegacyForms(t *testing.T) {
	for _, content := range []string{
		"**Path**: /w/x\n**Repo**: react\n**Branch**: b\n**Created by session**: s1",
		"Path: /w/x\nRepo: react\nBranch: b\nCreated by session: s1",
		"- Path: /w/x\n- Repo: react\n- Branch: b\n- Created by session: s1",
		"```json\n{\"path\": \"/w/x\", \"repo\": \"react\", \"branch\": \"b\", \"created_by_session\": \"s1\"}\n```",
	} {
		wt, ok := parseWorktree(content)
		if !ok || wt != (worktree{Path: "/w/x", Repo: "react", Branch: "b", CreatedBySession: "s1"}) {
			t.Errorf("parseWorktree(%q) = %+v, %v", content, wt, ok)
		}
	}
	if _, ok := parseWorktree("Worktree /w/x removido."); ok {
		t.Error("prose accepted as a worktree record")
	}
}

func TestParseStatus(t *testing.T) {
	for in, want := range map[string]string{"review\n\nnotes": "review", "**done**": "done", "In-Progress": "in-progress"} {
		if got, ok := parseStatus(in); !ok || got != want {
			t.Errorf("parseStatus(%q) = %q, %v", in, got, ok)
		}
	}
	if got, ok := parseStatus("cerrado"); ok || got != "todo" {
		t.Errorf("parseStatus(cerrado) = %q, %v", got, ok)
	}
}

func TestParseControlKey(t *testing.T) {
	cases := map[string]controlRef{
		"task/TRIPULSE-1/identity":         {kind: kindIdentity, taskRef: "TRIPULSE-1"},
		"task/none/session/abc":            {kind: kindSession, taskRef: "none", session: "abc"},
		"task/x/wt/base":                   {kind: kindWorktree, taskRef: "x"},
		"task/x/handoff/abc":               {kind: kindHandoff, taskRef: "x", session: "abc"},
		"task/x/progress/abc/impl-2":       {kind: kindProgress, taskRef: "x", session: "abc", slot: "impl-2"},
		"task/x/superseded/session/abc":    {},
		"architecture/engram-tasks-design": {},
	}
	for in, want := range cases {
		if got, _ := parseControlKey(in); got != want {
			t.Errorf("parseControlKey(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParentHints(t *testing.T) {
	cases := map[string]string{
		"Microservices (react) · parent TRIPULSE-3652":           "TRIPULSE-3652",
		"Portal / Administración (padre TRIPULSE-3242)":          "TRIPULSE-3242",
		"Socios de negocio (subtarea de TRIPULSE-3945)":          "TRIPULSE-3945",
		"Socios de negocio (epic TRIPULSE-3945)":                 "TRIPULSE-3945",
		"Socios de negocio SAP → GCP (épica TRIPULSE-3945)":      "TRIPULSE-3945",
		"Socios de negocio: SAP → GCP → canales (TRIPULSE-3945)": "",
		"Epicentro TRIPULSE-1":                                   "",
	}
	for product, want := range cases {
		if got := parseIdentity("**Product**: " + product).parentHintedKey; got != want {
			t.Errorf("hint in %q = %q, want %q", product, got, want)
		}
	}
}

func TestNewIDEncodesULIDTimestamp(t *testing.T) {
	id := newID(timeAt(1469918176385))
	if len(id) != 26 || id[:10] != "01ARYZ6S41" {
		t.Errorf("id = %q", id)
	}
}

func timeAt(ms int64) time.Time { return time.UnixMilli(ms) }

func TestParseProgressToleratesLegacyShapes(t *testing.T) {
	content := "```json\n{\"status\": \"blocked\", \"summary\": [\"a\", {\"k\": 1}], \"next_steps\": \"run ```go test```\"}\n```\n**Task**: X — y"
	role, p, ok := parseProgress("review-3", content)
	if !ok || role != "review" || p.Status != "blocked" {
		t.Fatalf("got %q %+v %v", role, p, ok)
	}
	if len(p.Summary) != 2 || p.Summary[1] != `{"k": 1}` || len(p.NextSteps) != 1 || p.NextSteps[0] != "run ```go test```" {
		t.Errorf("lists = %q %q", p.Summary, p.NextSteps)
	}
	if _, _, ok := parseProgress("writer-1", content); ok {
		t.Error("unknown role accepted")
	}
}

func TestProductParentRule(t *testing.T) {
	cases := map[string]string{
		"TRIPULSE-3945 Socios de negocio":                        "TRIPULSE-3945",
		"Integración de socios de negocio (integrator)":          "TRIPULSE-3945",
		"Socios de negocio: SAP → GCP → canales (TRIPULSE-3945)": "TRIPULSE-3945",
		"Microservices (react)":                                  "",
	}
	for product, want := range cases {
		if got := productParentKey(product); got != want {
			t.Errorf("productParentKey(%q) = %q, want %q", product, got, want)
		}
	}
}
