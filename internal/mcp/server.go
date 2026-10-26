// Package mcp is the stdio MCP server, a thin client of the HTTP API.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rcrespodev/neugram/internal/client"
)

const serverName = "neugram"

// Server serves the step-3 tools over MCP and forwards them to the daemon.
type Server struct {
	client *client.Client
	// ensure makes sure a daemon answers; it never replaces a live one.
	ensure func() error
}

func New(c *client.Client) *Server {
	return &Server{client: c, ensure: func() error { return c.EnsureDaemon(client.Policy{}) }}
}

func (s *Server) build(version string) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: serverName, Version: version}, nil)
	for _, t := range tools {
		srv.AddTool(&sdk.Tool{Name: t.name, Description: t.desc, InputSchema: t.inputSchema()}, s.handler(t.name))
	}
	return srv
}

// Run serves one MCP session over in/out until the client disconnects.
func (s *Server) Run(ctx context.Context, in io.ReadCloser, out io.WriteCloser, version string) error {
	return s.build(version).Run(ctx, &sdk.IOTransport{Reader: in, Writer: out})
}

func (s *Server) handler(name string) sdk.ToolHandler {
	return func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if err := s.ensure(); err != nil {
			return errorResult(&client.APIError{
				Code:    "internal",
				Message: "the neugram daemon is unreachable: " + err.Error(),
				Hint:    "run `neugram serve` or check $NEUGRAM_ADDR and $NEUGRAM_HOME/hooks.log",
			}), nil
		}
		var args map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult(&client.APIError{Code: "invalid_request", Message: "arguments are not a JSON object: " + err.Error()}), nil
			}
		}
		var out json.RawMessage
		err := s.call(name, args, &out)
		var apiErr *client.APIError
		switch {
		case err == nil:
			res := textResult(out, false)
			var structured map[string]any
			if json.Unmarshal(out, &structured) == nil {
				res.StructuredContent = structured
			}
			return res, nil
		case errors.As(err, &apiErr):
			return errorResult(apiErr), nil
		default:
			return errorResult(&client.APIError{
				Code:    "internal",
				Message: err.Error(),
				Hint:    "the daemon did not answer; check $NEUGRAM_HOME/hooks.log",
			}), nil
		}
	}
}

// call routes a tool to the daemon; tools without a handler yet report not_implemented.
func (s *Server) call(name string, args map[string]any, out *json.RawMessage) error {
	switch name {
	case "task_list":
		return s.client.Get("/tasks"+queryOf(without(args, "session_id")), out)
	case "task_pin", "task_status", "task_show", "worktree_register", "handoff_save":
		return s.callFlow(name, args, out)
	case "mem_save":
		return s.memSave(args, out)
	case "mem_update":
		return s.memUpdate(args, out)
	case "mem_search":
		return s.memSearch(args, out)
	case "mem_judge":
		return s.memJudge(args, out)
	}
	return &client.APIError{Code: "not_implemented", Message: name + " is not implemented yet"}
}

func queryOf(args map[string]any) string {
	q := url.Values{}
	for k, v := range args {
		switch x := v.(type) {
		case string:
			q.Set(k, x)
		case bool:
			q.Set(k, strconv.FormatBool(x))
		case float64:
			q.Set(k, strconv.FormatFloat(x, 'f', -1, 64))
		case []any:
			parts := make([]string, 0, len(x))
			for _, p := range x {
				parts = append(parts, fmt.Sprint(p))
			}
			q.Set(k, strings.Join(parts, ","))
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func textResult(raw []byte, isError bool) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}, IsError: isError}
}

func errorResult(e *client.APIError) *sdk.CallToolResult {
	raw, _ := json.Marshal(map[string]any{"error": e})
	return textResult(raw, true)
}
