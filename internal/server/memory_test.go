package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

type saveBody struct {
	SyncID           string `json:"sync_id"`
	TaskKey          string `json:"task_key"`
	JudgmentRequired bool   `json:"judgment_required"`
	Candidates       []struct {
		JudgmentID     string `json:"judgment_id"`
		SyncID         string `json:"sync_id"`
		Relation       string `json:"relation"`
		JudgmentStatus string `json:"judgment_status"`
	} `json:"candidates"`
}

func (f *fixture) save(session, title string, extra map[string]any) saveBody {
	f.t.Helper()
	in := map[string]any{"session_id": session, "title": title, "type": "decision",
		"content": "**What**: " + title, "task_id": "EPIC-1"}
	for k, v := range extra {
		in[k] = v
	}
	var out saveBody
	f.post("/observations", in, http.StatusCreated, &out)
	return out
}

func (f *fixture) postError(path string, body any, want int) errorBody {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(f.http.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		f.t.Fatalf("POST %s: status %d, want %d", path, resp.StatusCode, want)
	}
	var e errorBody
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		f.t.Fatal(err)
	}
	return e
}

func TestSaveAndJudgeFlow(t *testing.T) {
	f := newFixture(t)
	first := f.save("s1", "Triggers de profundidad en tasks", nil)
	if first.JudgmentRequired || first.TaskKey != "EPIC-1" {
		t.Fatalf("first = %+v", first)
	}
	second := f.save("s1", "Triggers de profundidad en tasks", map[string]any{"content": "segunda version", "topic_key": "triggers"})
	if !second.JudgmentRequired || len(second.Candidates) == 0 || second.Candidates[0].JudgmentID == "" {
		t.Fatalf("second = %+v", second)
	}
	for _, c := range second.Candidates {
		if c.JudgmentStatus != "pending" || c.Relation != "pending" {
			t.Fatalf("candidate = %+v", c)
		}
		f.post("/conflicts/judge", map[string]any{"session_id": "s1", "judgment_id": c.JudgmentID,
			"relation": "supersedes", "confidence": 0.9}, http.StatusOK, nil)
	}
	third := f.save("s1", "Triggers de profundidad en tasks", map[string]any{"content": "tercera version", "topic_key": "triggers"})
	if third.SyncID != second.SyncID || third.JudgmentRequired || len(third.Candidates) != 1 ||
		third.Candidates[0].JudgmentStatus != "judged" || third.Candidates[0].Relation != "supersedes" ||
		third.Candidates[0].JudgmentID != second.Candidates[0].JudgmentID {
		t.Fatalf("third = %+v", third)
	}
}

func TestSaveRejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		extra  map[string]any
		status int
		code   string
	}{
		{"no session", nil, http.StatusBadRequest, "invalid_request"},
		{"handoff kind", map[string]any{"session_id": "s1", "kind": "handoff"}, http.StatusBadRequest, "invalid_request"},
		{"no pin", map[string]any{"session_id": "s2"}, http.StatusUnprocessableEntity, "no_pin"},
		{"unknown field", map[string]any{"session_id": "s1", "impact": "high"}, http.StatusUnprocessableEntity, "invalid_field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{"title": "t", "content": "c", "type": "decision"}
			for k, v := range c.extra {
				body[k] = v
			}
			e := f.postError("/observations", body, c.status)
			if e.Error.Code != c.code {
				t.Fatalf("code = %q, want %q", e.Error.Code, c.code)
			}
			if c.code == "no_pin" && len(e.Error.Details.OpenTasks) == 0 {
				t.Fatal("no open_tasks in details")
			}
		})
	}
}

func TestJudgeRequiresSession(t *testing.T) {
	f := newFixture(t)
	e := f.postError("/conflicts/judge", map[string]any{"judgment_id": "rel-x", "relation": "related"}, http.StatusBadRequest)
	if e.Error.Code != "invalid_request" {
		t.Fatalf("code = %q", e.Error.Code)
	}
}
