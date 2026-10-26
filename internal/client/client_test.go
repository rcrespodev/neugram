package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("v1")
	c.Addr = strings.TrimPrefix(srv.URL, "http://")
	return c
}

func TestBodyMethodsSendJSONAndDecodeTheReply(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != method || r.Header.Get("Content-Type") != "application/json" || string(body) != `{"title":"x"}` {
					t.Errorf("%s %s %q %q", r.Method, r.Header.Get("Content-Type"), body, r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]string{"id": "T1"})
			}))
			var out struct{ ID string }
			in := map[string]string{"title": "x"}
			var err error
			switch method {
			case http.MethodPost:
				err = c.Post("/tasks", in, &out)
			case http.MethodPatch:
				err = c.Patch("/tasks", in, &out)
			default:
				err = c.Put("/tasks", in, &out)
			}
			if err != nil || out.ID != "T1" {
				t.Fatalf("out %+v, err %v", out, err)
			}
		})
	}
}

func TestErrorEnvelopeKeepsCodeHintAndDetails(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":{"code":"no_pin","message":"sin pin","hint":"task_pin","details":{"open_tasks":["A","B"]}}}`)
	}))
	err := c.Post("/x", map[string]string{}, nil)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v", err)
	}
	var d struct {
		OpenTasks []string `json:"open_tasks"`
	}
	if ae.Code != "no_pin" || ae.Hint != "task_pin" || json.Unmarshal(ae.Details, &d) != nil || len(d.OpenTasks) != 2 {
		t.Fatalf("%+v details %s", ae, ae.Details)
	}
}

func TestNonEnvelopeErrorReportsMethodAndStatus(t *testing.T) {
	c := testClient(t, http.NotFoundHandler())
	err := c.Put("/x", map[string]string{}, nil)
	if err == nil || !strings.Contains(err.Error(), "PUT /x: HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}
