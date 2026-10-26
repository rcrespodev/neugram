package server

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/rcrespodev/neugram/internal/store"
)

func (s *Server) routesMemory() {
	s.mux.HandleFunc("POST /observations", s.saveObservation)
	s.mux.HandleFunc("POST /conflicts/judge", s.judgeConflict)
}

func badRequest(msg string) error {
	return &store.Error{Code: "invalid_request", Message: msg}
}

// decodeFields reads a JSON object and rejects any key outside allowed.
func decodeFields(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := decode(w, r, &raw); err != nil {
		return nil, err
	}
	for k := range raw {
		if !slices.Contains(allowed, k) {
			return nil, &store.Error{Code: "invalid_field", Message: "unknown field " + k}
		}
	}
	return raw, nil
}

func fieldString(raw map[string]json.RawMessage, key string) (string, error) {
	v, ok := raw[key]
	if !ok {
		return "", nil
	}
	var out *string
	if err := json.Unmarshal(v, &out); err != nil {
		return "", badRequest(key + " must be a string")
	}
	if out == nil {
		return "", nil
	}
	return *out, nil
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type candidateBody struct {
	JudgmentID     string  `json:"judgment_id"`
	SyncID         string  `json:"sync_id"`
	Title          string  `json:"title"`
	Type           string  `json:"type"`
	Project        string  `json:"project"`
	CreatedAt      string  `json:"created_at"`
	Similarity     float64 `json:"similarity"`
	Relation       string  `json:"relation"`
	JudgmentStatus string  `json:"judgment_status"`
	Preview        string  `json:"preview"`
}

func (s *Server) saveObservation(w http.ResponseWriter, r *http.Request) {
	raw, err := decodeFields(w, r, "session_id", "title", "content", "type", "task_id", "topic_key",
		"scope", "project", "kind", "tool_name", "capture_prompt")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in store.NewObservation
	fields := map[string]*string{"session_id": &in.SessionID, "title": &in.Title, "content": &in.Content,
		"type": &in.Type, "task_id": &in.TaskID, "topic_key": &in.TopicKey, "scope": &in.Scope,
		"project": &in.Project, "kind": &in.Kind, "tool_name": &in.ToolName}
	for key, dst := range fields {
		if *dst, err = fieldString(raw, key); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if in.SessionID == "" {
		s.fail(w, r, badRequest("session_id is required"))
		return
	}
	if in.Kind == store.KindHandoff {
		s.fail(w, r, &store.Error{Code: "invalid_request", Message: "kind handoff is written by handoff_save, not by mem_save",
			Hint: "usá handoff_save (PUT /sessions/{id}/handoff)"})
		return
	}
	res, err := s.st.AddObservation(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cands := make([]candidateBody, len(res.Candidates))
	pending := false
	for i, c := range res.Candidates {
		cands[i] = candidateBody{c.JudgmentID, c.SyncID, c.Title, c.Type, c.Project, c.CreatedAt, c.Similarity,
			c.Relation, c.JudgmentStatus, c.Preview}
		pending = pending || c.JudgmentStatus == store.StatusPending
	}
	o := res.Observation
	writeJSON(w, http.StatusCreated, map[string]any{
		"sync_id": o.SyncID, "type": o.Type, "title": o.Title, "project": o.Project, "scope": o.Scope,
		"topic_key": nullIfEmpty(o.TopicKey), "kind": nullIfEmpty(o.Kind), "task_id": nullIfEmpty(o.TaskID),
		"task_key": nullIfEmpty(res.TaskKey), "session_id": o.SessionID, "created_at": o.CreatedAt,
		"footer_rewritten": res.FooterRewritten, "deduped": res.Deduped,
		"judgment_required": pending, "candidates": cands,
	})
}

func (s *Server) judgeConflict(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID  string   `json:"session_id"`
		JudgmentID string   `json:"judgment_id"`
		Relation   string   `json:"relation"`
		Reason     string   `json:"reason"`
		Evidence   string   `json:"evidence"`
		Confidence *float64 `json:"confidence"`
	}
	if err := decode(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.SessionID == "" {
		s.fail(w, r, badRequest("session_id is required"))
		return
	}
	j, err := s.st.JudgeRelation(r.Context(), store.JudgmentInput{JudgmentID: in.JudgmentID, Relation: in.Relation,
		Reason: in.Reason, Evidence: in.Evidence, Confidence: in.Confidence, SessionID: in.SessionID})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"judgment_id": j.JudgmentID, "source_sync_id": j.SourceSyncID, "target_sync_id": j.TargetSyncID,
		"relation": j.Relation, "judgment_status": j.JudgmentStatus, "confidence": j.Confidence,
		"session_id": j.SessionID, "superseded_relation_id": nullIfEmpty(j.SupersededRelationID),
		"updated_at": j.UpdatedAt,
	})
}
