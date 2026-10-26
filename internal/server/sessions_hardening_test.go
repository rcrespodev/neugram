package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateSessionRejectsRelativeDirectory(t *testing.T) {
	f := newFixture(t)
	var e errorBody
	f.post("/sessions", map[string]string{"id": "rel", "directory": "relative/dir"}, http.StatusBadRequest, &e)
	if e.Error.Code != "invalid_request" {
		t.Fatalf("error = %+v", e.Error)
	}
}

func TestConcurrentCreateSessionYieldsOneRow(t *testing.T) {
	f := newFixture(t)
	body, _ := json.Marshal(map[string]string{"id": "race-1", "directory": t.TempDir()})
	statuses := make(chan int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			resp, err := http.Post(f.http.URL+"/sessions", "application/json", bytes.NewReader(body))
			if err != nil {
				statuses <- 0
				return
			}
			resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	close(start)
	a, b := <-statuses, <-statuses
	if !(a == http.StatusCreated && b == http.StatusOK || a == http.StatusOK && b == http.StatusCreated) {
		t.Fatalf("statuses = %d, %d", a, b)
	}
}

func TestSessionRoutesRejectNonLoopbackHost(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/sessions", "/prompts", "/sessions/s2/end", "/sessions/s2/pin"} {
		req, _ := http.NewRequest("POST", f.http.URL+path, bytes.NewReader([]byte("{}")))
		req.Host = "evil.example.com"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var e errorBody
		json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || e.Error.Code != "invalid_request" {
			t.Errorf("%s: status %d error %+v", path, resp.StatusCode, e.Error)
		}
	}
}
