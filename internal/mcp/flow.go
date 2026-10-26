package mcp

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/rcrespodev/neugram/internal/client"
)

type flowRoute struct {
	method, path string
	body         map[string]any
}

func invalid(msg, hint string) error {
	return &client.APIError{Code: "invalid_request", Message: msg, Hint: hint}
}

func taskPath(args map[string]any, suffix string) (string, error) {
	key, _ := args["task_id"].(string)
	if key == "" {
		return "", invalid("task_id is required", "the daemon has no route that resolves the session pin; pass task_id")
	}
	return "/tasks/" + url.PathEscape(key) + suffix, nil
}

func sessionPath(args map[string]any, suffix string) (string, error) {
	id, _ := args["session_id"].(string)
	if id == "" {
		return "", invalid("session_id is required", "use the CLAUDE_CODE_SESSION_ID the session-start hook printed")
	}
	return "/sessions/" + url.PathEscape(id) + suffix, nil
}

func without(args map[string]any, drop ...string) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
}

// flowRouteOf maps each flow tool's arguments to its daemon route.
func flowRouteOf(name string, args map[string]any) (flowRoute, error) {
	switch name {
	case "task_pin":
		p, err := sessionPath(args, "/pin")
		if err != nil {
			return flowRoute{}, err
		}
		body := without(args, "session_id")
		body["pinned_by"] = "agent"
		if t, ok := body["tracker"].(string); ok {
			if body["tracker"], err = parseTracker(t); err != nil {
				return flowRoute{}, err
			}
		}
		return flowRoute{"POST", p, body}, nil
	case "task_status":
		p, err := taskPath(args, "/status")
		if err != nil {
			return flowRoute{}, err
		}
		return flowRoute{"PUT", p, without(args, "task_id")}, nil
	case "task_show":
		p, err := taskPath(args, "")
		if err != nil {
			return flowRoute{}, err
		}
		return flowRoute{"GET", p + queryOf(without(args, "task_id", "session_id")), nil}, nil
	case "worktree_register":
		return flowRoute{"POST", "/worktrees", without(args)}, nil
	case "handoff_save":
		p, err := sessionPath(args, "/handoff")
		if err != nil {
			return flowRoute{}, err
		}
		return flowRoute{"PUT", p, without(args, "session_id")}, nil
	}
	return flowRoute{}, nil
}

// parseTracker splits clickup:<id>:<url>; the url keeps its own colons.
func parseTracker(s string) (map[string]string, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, invalid("tracker must be clickup:<id>:<url>", "")
	}
	return map[string]string{"kind": parts[0], "id": parts[1], "url": parts[2]}, nil
}

func (s *Server) callFlow(name string, args map[string]any, out *json.RawMessage) error {
	rt, err := flowRouteOf(name, args)
	if err != nil {
		return err
	}
	switch rt.method {
	case "GET":
		return s.client.Get(rt.path, out)
	case "PUT":
		return s.client.Put(rt.path, rt.body, out)
	}
	return s.client.Post(rt.path, rt.body, out)
}
