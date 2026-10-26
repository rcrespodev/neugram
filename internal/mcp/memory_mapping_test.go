package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestJudgedCandidateClosesTheConflictOnTheNextSave(t *testing.T) {
	e := newMemoryEnv(t)
	save := map[string]any{"session_id": "s1", "title": "Triggers de profundidad en tasks", "type": "decision", "content": "**What**: primera"}
	e.call(t, "mem_save", save)
	save["content"], save["topic_key"] = "**What**: segunda version", "triggers"
	_, out := e.call(t, "mem_save", save)
	id := out["candidates"].([]any)[0].(map[string]any)["judgment_id"].(string)
	e.call(t, "mem_judge", map[string]any{"session_id": "s1", "judgment_id": id, "relation": "supersedes", "confidence": 0.9})
	save["content"] = "**What**: tercera version"
	_, out = e.call(t, "mem_save", save)
	cand := out["candidates"].([]any)[0].(map[string]any)
	if out["judgment_required"] != false || cand["judgment_status"] != "judged" || cand["relation"] != "supersedes" {
		t.Fatalf("third save = %v", out)
	}
}

func TestStructuredContentEqualsTheText(t *testing.T) {
	e := newMemoryEnv(t)
	res, out := e.call(t, "mem_save", map[string]any{"session_id": "s1", "title": "Algo", "type": "decision", "content": "**What**: uno"})
	got, _ := json.Marshal(res.StructuredContent)
	want, _ := json.Marshal(out)
	if string(got) != string(want) {
		t.Fatalf("structured = %s, text = %s", got, want)
	}
}

func TestSearchArgumentsBecomeTheHTTPQuery(t *testing.T) {
	var got url.Values
	cs := connect(t, daemonAt(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		io.WriteString(w, `{}`)
	})))
	callText(t, cs, "mem_search", map[string]any{
		"query": "zanahoria", "where": map[string]any{"status": "open"}, "full": true,
		"include_subtasks": true, "include_ancestors": false, "limit": 5, "offset": 10,
		"match_mode": "any", "type": []string{"decision", "bugfix"}, "kind": nil,
	})
	want := map[string]string{
		"q": "zanahoria", "where": `{"status":"open"}`, "full": "true", "include_subtasks": "true",
		"include_ancestors": "false", "limit": "5", "offset": "10", "match_mode": "any", "type": "decision,bugfix",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if got.Has("query") || got.Has("kind") {
		t.Errorf("unexpected params: %v", got)
	}
}

func TestUpdateEscapesTheSyncIDAndRequiresIt(t *testing.T) {
	var gotPath, gotBody string
	cs := connect(t, daemonAt(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		io.WriteString(w, `{}`)
	})))
	callText(t, cs, "mem_update", map[string]any{"session_id": "s", "sync_id": "a/b c?d", "title": "nuevo"})
	if gotPath != "/observations/a%2Fb%20c%3Fd" || strings.Contains(gotBody, "sync_id") {
		t.Fatalf("path = %s body = %s", gotPath, gotBody)
	}
	res, text := callText(t, cs, "mem_update", map[string]any{"session_id": "s", "sync_id": ""})
	if !res.IsError || !strings.Contains(text, `"invalid_request"`) {
		t.Fatalf("res = %v %s", res.IsError, text)
	}
}
