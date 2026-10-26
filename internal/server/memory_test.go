package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
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

type searchBody struct {
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
	Results   []struct {
		SyncID  string  `json:"sync_id"`
		TaskKey string  `json:"task_key"`
		Preview *string `json:"preview"`
		Content *string `json:"content"`
	} `json:"results"`
}

func TestSearchWithTaskAndSubtasks(t *testing.T) {
	f := newFixture(t)
	var direct, deep searchBody
	f.get("/observations?task_id=EPIC-1", http.StatusOK, &direct)
	f.get("/observations?task_id=EPIC-1&include_subtasks=true", http.StatusOK, &deep)
	if direct.Total != 2 || deep.Total != 3 {
		t.Fatalf("direct = %d, deep = %d", direct.Total, deep.Total)
	}
	for _, r := range deep.Results {
		if r.SyncID == "obs-3" && r.TaskKey != "EPIC-1-a-x" {
			t.Fatalf("task_key = %q", r.TaskKey)
		}
	}
}

func TestSearchFullSwitchesPreviewForContent(t *testing.T) {
	f := newFixture(t)
	var short, full searchBody
	f.get("/observations?q=Grandchild", http.StatusOK, &short)
	f.get("/observations?q=Grandchild&full=true", http.StatusOK, &full)
	if len(short.Results) != 1 || short.Results[0].Preview == nil || short.Results[0].Content != nil {
		t.Fatalf("short = %+v", short)
	}
	if full.Results[0].Content == nil || full.Results[0].Preview != nil {
		t.Fatalf("full = %+v", full)
	}
}

func TestSearchRejectsUnknownWhereColumn(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.http.URL + `/observations?where={"impact":"high"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e errorBody
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != "invalid_field" {
		t.Fatalf("status %d, error %+v", resp.StatusCode, e.Error)
	}
}

func (f *fixture) patch(path string, body any, want int, out any) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, f.http.URL+path, bytes.NewReader(raw))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		f.t.Fatalf("PATCH %s: status %d, want %d", path, resp.StatusCode, want)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func TestPatchTaskRewritesTheFooter(t *testing.T) {
	f := newFixture(t)
	saved := f.save("s1", "Decision movible", nil)
	var out struct {
		TaskKey string   `json:"task_key"`
		Changed []string `json:"changed"`
		Footer  bool     `json:"footer_rewritten"`
	}
	f.patch("/observations/"+saved.SyncID, map[string]any{"session_id": "s1", "task_id": "EPIC-1-a"}, http.StatusOK, &out)
	if out.TaskKey != "EPIC-1-a" || !out.Footer || !slices.Contains(out.Changed, "task_id") || !slices.Contains(out.Changed, "content") {
		t.Fatalf("out = %+v", out)
	}
}

func TestPatchRequiresSessionAndAField(t *testing.T) {
	f := newFixture(t)
	f.patch("/observations/obs-1", map[string]any{"title": "x"}, http.StatusBadRequest, nil)
	f.patch("/observations/obs-1", map[string]any{"session_id": "s1"}, http.StatusBadRequest, nil)
	f.patch("/observations/obs-1", map[string]any{"session_id": "nope", "title": "x"}, http.StatusNotFound, nil)
	f.patch("/observations/obs-zz", map[string]any{"session_id": "s1", "title": "x"}, http.StatusNotFound, nil)
}

func TestJudgeRejectsUnknownField(t *testing.T) {
	f := newFixture(t)
	e := f.postError("/conflicts/judge", map[string]any{"session_id": "s1", "judgment_id": "rel-x", "relation": "related", "bogus": 1}, http.StatusUnprocessableEntity)
	if e.Error.Code != "invalid_field" {
		t.Fatalf("code = %q", e.Error.Code)
	}
}

func TestWriteRoutesRejectInvalidAndOversizedBodies(t *testing.T) {
	f := newFixture(t)
	huge := `{"session_id":"s1","title":"` + string(bytes.Repeat([]byte("a"), maxBodyBytes)) + `"}`
	routes := []struct{ method, path string }{
		{http.MethodPost, "/observations"},
		{http.MethodPatch, "/observations/obs-1"},
		{http.MethodPost, "/conflicts/judge"},
	}
	for _, rt := range routes {
		for name, body := range map[string]string{"invalid json": "{nope", "oversized": huge} {
			req, _ := http.NewRequest(rt.method, f.http.URL+rt.path, bytes.NewReader([]byte(body)))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var e errorBody
			json.NewDecoder(resp.Body).Decode(&e)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest || e.Error.Code != "invalid_request" {
				t.Fatalf("%s %s %s: status %d, error %+v", rt.method, rt.path, name, resp.StatusCode, e.Error)
			}
		}
	}
}

func TestSearchRejectsBadQueryParams(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"limit=0", "limit=201", "limit=-1", "limit=abc", "offset=-1", "full=maybe",
		"include_ancestors=maybe", "include_subtasks=2x"} {
		resp, err := http.Get(f.http.URL + "/observations?" + q)
		if err != nil {
			t.Fatal(err)
		}
		var e errorBody
		json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || e.Error.Code != "invalid_request" {
			t.Fatalf("%s: status %d, error %+v", q, resp.StatusCode, e.Error)
		}
	}
}

func TestPatchNullClearsTopicKeyAndKind(t *testing.T) {
	f := newFixture(t)
	saved := f.save("s1", "Con topic", map[string]any{"topic_key": "tk-1"})
	var out struct {
		TopicKey *string `json:"topic_key"`
		Kind     *string `json:"kind"`
	}
	f.patch("/observations/"+saved.SyncID, map[string]any{"session_id": "s1", "topic_key": nil}, http.StatusOK, &out)
	if out.TopicKey != nil {
		t.Fatalf("topic_key = %q, want null", *out.TopicKey)
	}
	f.patch("/observations/"+saved.SyncID, map[string]any{"session_id": "s1", "kind": nil, "title": "x"}, http.StatusOK, &out)
	if out.Kind != nil {
		t.Fatalf("kind = %q, want null", *out.Kind)
	}
}

func TestJudgeRequiresSession(t *testing.T) {
	f := newFixture(t)
	e := f.postError("/conflicts/judge", map[string]any{"judgment_id": "rel-x", "relation": "related"}, http.StatusBadRequest)
	if e.Error.Code != "invalid_request" {
		t.Fatalf("code = %q", e.Error.Code)
	}
}
