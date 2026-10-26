package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rcrespodev/neugram/internal/client"
)

func connect(t *testing.T, s *Server) *sdk.ClientSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx, inR, outW, "test")
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return cs
}

func daemonAt(t *testing.T, h http.Handler) *Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := client.New("test")
	c.Addr = strings.TrimPrefix(srv.URL, "http://")
	return &Server{client: c, ensure: func() error { return nil }}
}

func callText(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res, res.Content[0].(*sdk.TextContent).Text
}

func TestInitializeNamesTheServerNeugram(t *testing.T) {
	cs := connect(t, daemonAt(t, http.NotFoundHandler()))
	if got := cs.InitializeResult().ServerInfo.Name; got != "neugram" {
		t.Fatalf("server name = %q", got)
	}
}

func TestToolsListSnapshotOfNamesAndRequired(t *testing.T) {
	cs := connect(t, daemonAt(t, http.NotFoundHandler()))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, tool := range res.Tools {
		schema := tool.InputSchema.(map[string]any)
		var req []string
		for _, r := range schema["required"].([]any) {
			req = append(req, r.(string))
		}
		sort.Strings(req)
		got[tool.Name] = req
		if _, ok := schema["properties"].(map[string]any)["session_id"]; !ok {
			t.Errorf("%s has no session_id property", tool.Name)
		}
	}
	want := map[string][]string{
		"mem_save":          {"content", "session_id", "title", "type"},
		"mem_search":        {"query"},
		"mem_update":        {"session_id", "sync_id"},
		"mem_judge":         {"judgment_id", "relation", "session_id"},
		"task_pin":          {"session_id", "task_id"},
		"task_list":         {},
		"task_status":       {"session_id", "status"},
		"task_show":         {},
		"worktree_register": {"branch", "path", "repo", "session_id"},
		"handoff_save":      {"content", "session_id"},
	}
	if len(got) != len(want) {
		t.Fatalf("tools = %v", got)
	}
	for name, req := range want {
		if strings.Join(got[name], ",") != strings.Join(req, ",") {
			t.Errorf("%s required = %v, want %v", name, got[name], req)
		}
	}
}

func TestUnreachableDaemonIsAnInternalError(t *testing.T) {
	s := daemonAt(t, http.NotFoundHandler())
	s.ensure = func() error { return errors.New("connection refused") }
	cs := connect(t, s)
	res, text := callText(t, cs, "task_list", nil)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	var env struct{ Error client.APIError }
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "internal" || env.Error.Hint == "" {
		t.Fatalf("error = %+v", env.Error)
	}
}

func TestDaemonErrorKeepsCodeHintAndDetails(t *testing.T) {
	s := daemonAt(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":"unknown_task","message":"no such task","hint":"open: A, B","details":{"task_id":"X"}}}`)
	}))
	cs := connect(t, s)
	res, text := callText(t, cs, "task_list", nil)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	var env struct{ Error client.APIError }
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "unknown_task" || env.Error.Hint != "open: A, B" || string(env.Error.Details) != `{"task_id":"X"}` {
		t.Fatalf("error = %+v details=%s", env.Error, env.Error.Details)
	}
}

func TestTaskListForwardsFiltersAndReturnsDaemonJSON(t *testing.T) {
	var gotQuery string
	s := daemonAt(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `{"total":0,"tasks":[]}`)
	}))
	cs := connect(t, s)
	res, text := callText(t, cs, "task_list", map[string]any{"product": "P", "include_done": true, "status": []string{"todo", "done"}})
	if res.IsError || text != `{"total":0,"tasks":[]}` {
		t.Fatalf("res = %v %s", res.IsError, text)
	}
	for _, part := range []string{"product=P", "include_done=true", "status=todo%2Cdone"} {
		if !strings.Contains(gotQuery, part) {
			t.Errorf("query %q lacks %q", gotQuery, part)
		}
	}
}

func TestUnimplementedToolReportsNotImplemented(t *testing.T) {
	cs := connect(t, daemonAt(t, http.NotFoundHandler()))
	res, text := callText(t, cs, "task_status", map[string]any{"session_id": "s", "status": "done"})
	if !res.IsError || !strings.Contains(text, `"not_implemented"`) {
		t.Fatalf("res = %v %s", res.IsError, text)
	}
}

func TestNewNeverAsksToReplaceTheDaemon(t *testing.T) {
	t.Setenv("NEUGRAM_HOME", t.TempDir())
	var shutdown bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shutdown" {
			shutdown = true
		}
		io.WriteString(w, `{"version":"other"}`)
	}))
	defer srv.Close()
	c := client.New("mine")
	c.Addr = strings.TrimPrefix(srv.URL, "http://")
	if err := New(c).ensure(); err != nil || shutdown {
		t.Fatalf("ensure err=%v shutdown=%v", err, shutdown)
	}
}
