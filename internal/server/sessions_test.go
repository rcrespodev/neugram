package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func (f *fixture) post(path string, body any, want int, out any) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(f.http.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var e errorBody
		json.NewDecoder(resp.Body).Decode(&e)
		f.t.Fatalf("POST %s: status %d, want %d (%+v)", path, resp.StatusCode, want, e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			f.t.Fatal(err)
		}
	}
}

type sessionBody struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Created   bool   `json:"created"`
	Pin       *struct {
		TaskKey  string `json:"task_key"`
		PinnedBy string `json:"pinned_by"`
	} `json:"pin"`
}

func TestCreateSessionIsIdempotentAndKeepsThePin(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	in := map[string]string{"id": "new-1", "directory": dir}
	var first, second sessionBody
	f.post("/sessions", in, http.StatusCreated, &first)
	if !first.Created || first.Pin != nil || first.Project == "" {
		t.Fatalf("first = %+v", first)
	}
	f.post("/sessions/new-1/pin", map[string]string{"task_id": "EPIC-1", "pinned_by": "hook"}, http.StatusOK, nil)
	f.post("/sessions", in, http.StatusOK, &second)
	if second.Created || second.Pin == nil || second.Pin.TaskKey != "EPIC-1" || second.Pin.PinnedBy != "hook" {
		t.Fatalf("second = %+v", second)
	}
}

func TestCreateSessionRejectsMissingFields(t *testing.T) {
	f := newFixture(t)
	f.post("/sessions", map[string]string{"id": "x"}, http.StatusBadRequest, nil)
}

func TestPinWithTitleCreatesTheTask(t *testing.T) {
	f := newFixture(t)
	var got struct {
		TaskKey string `json:"task_key"`
		Created bool   `json:"created"`
		Status  string `json:"status"`
	}
	f.post("/sessions/s2/pin", map[string]string{"task_id": "NEW-1", "title": "Nueva"}, http.StatusCreated, &got)
	if got.TaskKey != "NEW-1" || !got.Created || got.Status != "todo" {
		t.Fatalf("pin = %+v", got)
	}
	var e errorBody
	f.post("/sessions/s2/pin", map[string]string{"task_id": "NOPE-1"}, http.StatusNotFound, &e)
	if e.Error.Code != "unknown_task" || len(e.Error.Details.OpenTasks) == 0 {
		t.Fatalf("error = %+v", e.Error)
	}
	f.post("/sessions/s2/pin", map[string]string{"task_id": "EPIC-1", "title": "x"}, http.StatusOK, nil)
}

func TestWritesToAnUnknownSessionAre404(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		path string
		body any
	}{
		{"/sessions/ghost/end", map[string]string{"reason": "clear"}},
		{"/sessions/ghost/pin", map[string]string{"task_id": "none"}},
		{"/prompts", map[string]string{"session_id": "ghost", "content": "hola"}},
	} {
		var e errorBody
		f.post(c.path, c.body, http.StatusNotFound, &e)
		if e.Error.Code != "unknown_session" || e.Error.Hint == "" {
			t.Errorf("%s: error = %+v", c.path, e.Error)
		}
	}
}

func TestEndSessionStampsEndedAt(t *testing.T) {
	f := newFixture(t)
	f.post("/sessions/s2/end", map[string]string{"reason": "clear"}, http.StatusNoContent, nil)
	sess, err := f.srv.st.GetSession(t.Context(), "s2")
	if err != nil || sess.EndedAt == "" {
		t.Fatalf("session = %+v, err = %v", sess, err)
	}
}

func TestPromptDedupe(t *testing.T) {
	f := newFixture(t)
	body := map[string]string{"session_id": "s2", "content": "quiero definir la API"}
	var first, second struct {
		SyncID  string `json:"sync_id"`
		Project string `json:"project"`
		Deduped bool   `json:"deduped"`
	}
	f.post("/prompts", body, http.StatusCreated, &first)
	f.post("/prompts", body, http.StatusCreated, &second)
	if first.Deduped || !second.Deduped || first.SyncID != second.SyncID || first.Project != "neugram" {
		t.Fatalf("first = %+v second = %+v", first, second)
	}
	f.post("/prompts", map[string]string{"session_id": "s2"}, http.StatusBadRequest, nil)
}
