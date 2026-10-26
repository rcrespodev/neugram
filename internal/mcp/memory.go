package mcp

import (
	"encoding/json"
	"net/url"

	"github.com/rcrespodev/neugram/internal/client"
)

// searchParam maps mem_search arguments to the query names of GET /observations.
var searchParam = map[string]string{"query": "q"}

func (s *Server) memSave(args map[string]any, out *json.RawMessage) error {
	return s.client.Post("/observations", args, out)
}

func (s *Server) memJudge(args map[string]any, out *json.RawMessage) error {
	return s.client.Post("/conflicts/judge", args, out)
}

func (s *Server) memUpdate(args map[string]any, out *json.RawMessage) error {
	syncID, _ := args["sync_id"].(string)
	if syncID == "" {
		return &client.APIError{Code: "invalid_request", Message: "sync_id is required"}
	}
	body := map[string]any{}
	for k, v := range args {
		if k != "sync_id" {
			body[k] = v
		}
	}
	return s.client.Patch("/observations/"+url.PathEscape(syncID), body, out)
}

func (s *Server) memSearch(args map[string]any, out *json.RawMessage) error {
	mapped := map[string]any{}
	for k, v := range args {
		if where, ok := v.(map[string]any); ok && k == "where" {
			raw, _ := json.Marshal(where)
			v = string(raw)
		}
		if name, ok := searchParam[k]; ok {
			k = name
		}
		mapped[k] = v
	}
	return s.client.Get("/observations"+queryOf(mapped), out)
}
