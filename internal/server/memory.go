package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/rcrespodev/neugram/internal/store"
)

func (s *Server) routesMemory() {
	s.mux.HandleFunc("POST /observations", s.saveObservation)
	s.mux.HandleFunc("GET /observations", s.searchObservations)
	s.mux.HandleFunc("PATCH /observations/{sync_id}", s.updateObservation)
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
	raw, err := decodeFields(w, r, "session_id", "judgment_id", "relation", "reason", "evidence", "confidence")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct{ SessionID, JudgmentID, Relation, Reason, Evidence string }
	for key, dst := range map[string]*string{"session_id": &in.SessionID, "judgment_id": &in.JudgmentID,
		"relation": &in.Relation, "reason": &in.Reason, "evidence": &in.Evidence} {
		if *dst, err = fieldString(raw, key); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var confidence *float64
	if v, ok := raw["confidence"]; ok {
		if err := json.Unmarshal(v, &confidence); err != nil {
			s.fail(w, r, badRequest("confidence must be a number"))
			return
		}
	}
	if in.SessionID == "" {
		s.fail(w, r, badRequest("session_id is required"))
		return
	}
	j, err := s.st.JudgeRelation(r.Context(), store.JudgmentInput{JudgmentID: in.JudgmentID, Relation: in.Relation,
		Reason: in.Reason, Evidence: in.Evidence, Confidence: confidence, SessionID: in.SessionID})
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

func (s *Server) searchObservations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := store.SearchParams{
		Query: q.Get("q"), MatchMode: q.Get("match_mode"), Project: q.Get("project"), Scope: q.Get("scope"),
		Kind: q.Get("kind"), SessionID: q.Get("session_id"), Types: multi(q["type"]), Product: q.Get("product"),
		Since: q.Get("since"), Until: q.Get("until"), Task: q.Get("task_id"),
	}
	var err error
	if p.Limit, p.Offset, err = searchPage(q.Get("limit"), q.Get("offset")); err != nil {
		s.fail(w, r, err)
		return
	}
	ancestors, err := boolParam(q.Get("include_ancestors"), true, "include_ancestors")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.SkipAncestors = !ancestors
	if p.IncludeSubtasks, err = boolParam(q.Get("include_subtasks"), false, "include_subtasks"); err != nil {
		s.fail(w, r, err)
		return
	}
	full, err := boolParam(q.Get("full"), false, "full")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if raw := q.Get("where"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p.Where); err != nil {
			s.fail(w, r, badRequest("where must be a JSON object"))
			return
		}
	}
	results, total, err := s.st.SearchObservations(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit := p.Limit
	if limit == 0 {
		limit = store.DefaultSearchLimit
	}
	keys := map[string]string{}
	out := make([]map[string]any, len(results))
	for i, res := range results {
		key, err := s.taskKey(r.Context(), keys, res.TaskID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		item := map[string]any{
			"sync_id": res.SyncID, "title": res.Title, "type": res.Type, "project": res.Project, "scope": res.Scope,
			"topic_key": nullIfEmpty(res.TopicKey), "kind": nullIfEmpty(res.Kind), "task_id": nullIfEmpty(res.TaskID),
			"task_key": nullIfEmpty(key), "session_id": res.SessionID, "created_at": res.CreatedAt, "score": res.Score,
		}
		if full {
			item["content"] = res.Content
		} else {
			item["preview"] = res.Preview
		}
		out[i] = item
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query": p.Query, "total": total, "limit": limit, "offset": p.Offset,
		"truncated": total > limit+p.Offset, "results": out,
	})
}

func searchPage(limit, offset string) (l, o int, err error) {
	if limit != "" {
		if l, err = intParam(limit, "limit"); err != nil {
			return 0, 0, err
		}
		if l == 0 {
			return 0, 0, badRequest("limit must be between 1 and 200")
		}
	}
	if offset != "" {
		o, err = intParam(offset, "offset")
	}
	return l, o, err
}

func (s *Server) taskKey(ctx context.Context, cache map[string]string, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if key, ok := cache[id]; ok {
		return key, nil
	}
	key, err := s.st.TaskKeyByID(ctx, id)
	if err == nil {
		cache[id] = key
	}
	return key, err
}

func fieldPtr(raw map[string]json.RawMessage, key string) (*string, error) {
	if _, ok := raw[key]; !ok {
		return nil, nil
	}
	v, err := fieldString(raw, key)
	return &v, err
}

func (s *Server) updateObservation(w http.ResponseWriter, r *http.Request) {
	raw, err := decodeFields(w, r, "session_id", "title", "content", "type", "topic_key", "task_id", "scope", "kind")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sessionID, err := fieldString(raw, "session_id")
	if err == nil && sessionID == "" {
		err = badRequest("session_id is required")
	}
	if err == nil {
		_, err = s.st.GetSession(r.Context(), sessionID)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var p store.ObservationPatch
	for key, dst := range map[string]**string{"title": &p.Title, "content": &p.Content, "type": &p.Type,
		"topic_key": &p.TopicKey, "task_id": &p.TaskID, "scope": &p.Scope, "kind": &p.Kind} {
		if *dst, err = fieldPtr(raw, key); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	res, err := s.st.UpdateObservation(r.Context(), r.PathValue("sync_id"), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	o := res.Observation
	writeJSON(w, http.StatusOK, map[string]any{
		"sync_id": o.SyncID, "title": o.Title, "type": o.Type, "project": o.Project, "scope": o.Scope,
		"topic_key": nullIfEmpty(o.TopicKey), "kind": nullIfEmpty(o.Kind), "task_id": nullIfEmpty(o.TaskID),
		"task_key": nullIfEmpty(res.TaskKey), "changed": res.Changed, "footer_rewritten": res.FooterRewritten,
		"revision_count": o.RevisionCount, "updated_at": o.UpdatedAt,
	})
}
