package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rcrespodev/neugram/internal/client"
	"github.com/rcrespodev/neugram/internal/server"
	"github.com/rcrespodev/neugram/internal/store"
)

type memoryEnv struct {
	cs   *sdk.ClientSession
	http string
}

func newMemoryEnv(t *testing.T) *memoryEnv {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "neugram.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hs := httptest.NewServer(server.New(st, server.Options{Version: "test", DBPath: db}).Handler())
	t.Cleanup(hs.Close)
	c := client.New("test")
	c.Addr = strings.TrimPrefix(hs.URL, "http://")
	env := &memoryEnv{http: hs.URL}
	env.cs = connect(t, &Server{client: c, ensure: func() error { return nil }})
	env.post(t, "/sessions", map[string]any{"id": "s1", "project": "neugram", "directory": dir})
	env.post(t, "/sessions", map[string]any{"id": "s2", "project": "neugram", "directory": dir})
	env.post(t, "/sessions/s1/pin", map[string]any{"task_id": "T-1", "title": "Tarea uno"})
	return env
}

func (e *memoryEnv) post(t *testing.T, path string, body any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(e.http+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("POST %s: %d", path, resp.StatusCode)
	}
}

func (e *memoryEnv) call(t *testing.T, name string, args map[string]any) (*sdk.CallToolResult, map[string]any) {
	t.Helper()
	res, text := callText(t, e.cs, name, args)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: not JSON: %s", name, text)
	}
	return res, out
}

func TestSaveSimilarContentAsksForJudgmentAndJudgeClosesIt(t *testing.T) {
	e := newMemoryEnv(t)
	save := map[string]any{"session_id": "s1", "title": "Triggers de profundidad en tasks", "type": "decision", "content": "**What**: primera"}
	if res, out := e.call(t, "mem_save", save); res.IsError || out["judgment_required"] == true {
		t.Fatalf("first save = %v %v", res.IsError, out)
	}
	save["content"], save["topic_key"] = "**What**: segunda version", "triggers"
	res, out := e.call(t, "mem_save", save)
	if res.IsError || out["judgment_required"] != true {
		t.Fatalf("second save = %v %v", res.IsError, out)
	}
	if res.StructuredContent == nil {
		t.Fatal("no structuredContent")
	}
	id := out["candidates"].([]any)[0].(map[string]any)["judgment_id"].(string)
	res, out = e.call(t, "mem_judge", map[string]any{"session_id": "s1", "judgment_id": id, "relation": "supersedes", "confidence": 0.9})
	if res.IsError {
		t.Fatalf("judge = %v", out)
	}
}

func TestSaveWithoutPinOrTaskIsNoPinWithHint(t *testing.T) {
	e := newMemoryEnv(t)
	res, out := e.call(t, "mem_save", map[string]any{"session_id": "s2", "title": "t", "type": "decision", "content": "c"})
	errObj := out["error"].(map[string]any)
	if !res.IsError || errObj["code"] != "no_pin" || !strings.Contains(errObj["hint"].(string), "task_pin") {
		t.Fatalf("res = %v %v", res.IsError, out)
	}
}

func TestSearchByTaskReturnsWhatWasSaved(t *testing.T) {
	e := newMemoryEnv(t)
	e.call(t, "mem_save", map[string]any{"session_id": "s1", "title": "Guarde algo unico", "type": "discovery", "content": "**What**: zanahoria"})
	res, out := e.call(t, "mem_search", map[string]any{"query": "zanahoria", "task_id": "T-1", "type": []string{"discovery"}})
	if res.IsError || out["total"].(float64) != 1 {
		t.Fatalf("search = %v %v", res.IsError, out)
	}
}

func TestUpdateContentReportsChanged(t *testing.T) {
	e := newMemoryEnv(t)
	_, saved := e.call(t, "mem_save", map[string]any{"session_id": "s1", "title": "Algo", "type": "decision", "content": "**What**: uno"})
	res, out := e.call(t, "mem_update", map[string]any{"session_id": "s1", "sync_id": saved["sync_id"], "content": "**What**: dos"})
	raw, _ := json.Marshal(out["changed"])
	if res.IsError || !strings.Contains(string(raw), "content") {
		t.Fatalf("update = %v %v", res.IsError, out)
	}
}
