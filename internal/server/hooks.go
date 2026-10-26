package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rcrespodev/neugram/internal/hooks"
	"github.com/rcrespodev/neugram/internal/hooks/transcript"
	"github.com/rcrespodev/neugram/internal/store"
)

func (s *Server) routesHooks() {
	s.mux.HandleFunc("POST /hooks/session-start", s.hookSessionStart)
	s.mux.HandleFunc("POST /hooks/stop", func(w http.ResponseWriter, r *http.Request) { s.hookStop(w, r, false) })
	s.mux.HandleFunc("POST /hooks/subagent-stop", func(w http.ResponseWriter, r *http.Request) { s.hookStop(w, r, true) })
}

const maxTranscriptBytes = 64 << 20

type stopRequest struct {
	SessionID           string `json:"session_id"`
	TranscriptPath      string `json:"transcript_path"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
	StopHookActive      bool   `json:"stop_hook_active"`
}

func checkTranscriptPath(field, path string) error {
	bad := func(why string) error {
		return &store.Error{Code: "invalid_request", Message: field + " " + why}
	}
	if !filepath.IsAbs(path) {
		return bad("must be an absolute path")
	}
	if filepath.Ext(path) != ".jsonl" {
		return bad("must be a .jsonl file")
	}
	st, err := os.Stat(path)
	if err != nil {
		return bad("unreadable: " + err.Error())
	}
	if !st.Mode().IsRegular() {
		return bad("must be a regular file")
	}
	if st.Size() > maxTranscriptBytes {
		return bad("is larger than the 64 MiB limit")
	}
	return nil
}

type observed struct {
	FilesMutated     []string `json:"files_mutated"`
	Saves            int      `json:"saves"`
	WorktreesCreated []string `json:"worktrees_created"`
	ProgressReports  int      `json:"progress_reports"`
}

type stopResponse struct {
	Decision       *string  `json:"decision"`
	Reason         string   `json:"reason,omitempty"`
	Gate           *string  `json:"gate"`
	GatesEvaluated []string `json:"gates_evaluated"`
	Observed       observed `json:"observed"`
}

func (s *Server) hookStop(w http.ResponseWriter, r *http.Request, subagent bool) {
	var req stopRequest
	err := decode(w, r, &req)
	if err == nil && (req.SessionID == "" || req.TranscriptPath == "") {
		err = &store.Error{Code: "invalid_request", Message: "session_id and transcript_path are required"}
	}
	for _, f := range []struct{ name, path string }{{"transcript_path", req.TranscriptPath}, {"agent_transcript_path", req.AgentTranscriptPath}} {
		if err == nil && f.path != "" {
			err = checkTranscriptPath(f.name, f.path)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.st.GetSession(r.Context(), req.SessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := stopResponse{GatesEvaluated: []string{}, Observed: observed{FilesMutated: []string{}, WorktreesCreated: []string{}}}
	if req.StopHookActive {
		writeJSON(w, http.StatusOK, out)
		return
	}

	mode := transcript.Turn
	path := req.TranscriptPath
	if subagent {
		mode = transcript.Entire
		if req.AgentTranscriptPath != "" {
			path = req.AgentTranscriptPath
		}
	}
	turn, err := transcript.Scan(path, transcript.Options{Mode: mode})
	if err != nil {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "transcript unreadable: " + err.Error()})
		return
	}
	overrides, err := s.gateOverrides(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := hooks.Evaluate(hooks.TurnContext{
		SessionID: sess.ID, Pinned: sess.PinnedAt != "", OpenTasks: s.openTaskLines(r),
		Subagent: subagent, Turn: turn,
	}, overrides)

	out.GatesEvaluated = d.Evaluated
	out.Observed = observed{FilesMutated: hooks.MutatedFiles(turn), Saves: hooks.SaveCount(turn), WorktreesCreated: []string{}}
	for _, wt := range turn.Worktrees {
		out.Observed.WorktreesCreated = append(out.Observed.WorktreesCreated, wt.Path)
	}
	if d.Violation != nil {
		block := "block"
		out.Decision, out.Gate, out.Reason = &block, &d.Violation.Gate, d.Violation.Reason
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) gateOverrides(r *http.Request) (map[string]hooks.Override, error) {
	rows, err := s.st.ListGates(r.Context())
	if err != nil {
		return nil, err
	}
	out := map[string]hooks.Override{}
	for _, g := range rows {
		o := hooks.Override{Enabled: g.Enabled}
		if g.Params != "" {
			o.Params = json.RawMessage(g.Params)
		}
		out[g.Name] = o
	}
	return out, nil
}

func (s *Server) openTaskLines(r *http.Request) []string {
	open, _, err := s.st.ListTasks(r.Context(), store.TaskQuery{Limit: store.MaxLimit})
	if err != nil {
		return nil
	}
	lines := make([]string, len(open))
	for i, t := range open {
		lines[i] = t.TaskKey + " — " + t.Title + " [" + t.Status + "]"
	}
	return lines
}
