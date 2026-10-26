package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"
)

// DedupeWindow is the dedupe look-back defined in docs/specs/data-model.md.
const DedupeWindow = 15 * time.Minute

var ObservationTypes = []string{
	"bugfix", "decision", "architecture", "discovery", "pattern",
	"config", "preference", "handoff", "session_summary",
}

const (
	ScopeProject  = "project"
	ScopePersonal = "personal"
	KindHandoff   = "handoff"
)

type Observation struct {
	SyncID         string
	SessionID      string
	Type           string
	Title          string
	Content        string
	Project        string
	Scope          string
	TopicKey       string
	ToolName       string
	TaskID         string
	Kind           string
	NormalizedHash string
	RevisionCount  int
	DuplicateCount int
	LastSeenAt     string
	ReviewAfter    string
	Pinned         bool
	CreatedAt      string
	UpdatedAt      string
}

// NewObservation is the input of AddObservation. Empty strings are NULL.
type NewObservation struct {
	SessionID string
	Type      string
	Title     string
	Content   string
	Project   string
	Scope     string
	TopicKey  string
	ToolName  string
	TaskID    string
	Kind      string
}

type SaveResult struct {
	Observation Observation
	Deduped     bool
	Upserted    bool
	Candidates  []Candidate
}

// AddObservation writes an observation: upsert by topic_key when one is given,
// otherwise dedupe by normalized_hash within DedupeWindow, otherwise insert.
// FindCandidates runs in the same transaction over the written row, and each
// candidate gets a pending memory_relations row.
func (s *Store) AddObservation(ctx context.Context, in NewObservation) (SaveResult, error) {
	if in.Scope == "" {
		in.Scope = ScopeProject
	}
	if err := validateObservation(in.Type, in.Scope, in.Kind); err != nil {
		return SaveResult{}, err
	}
	if in.SessionID == "" || in.Title == "" || in.Content == "" || in.Project == "" {
		return SaveResult{}, errorf(ErrInvalidRequest, "session_id, title, content and project are required")
	}
	var res SaveResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := getSession(ctx, tx, in.SessionID); err != nil {
			return err
		}
		now := s.timestamp()
		hash := NormalizedHash(in.Content)
		id, err := s.writeObservation(ctx, tx, in, hash, now, &res)
		if err != nil {
			return err
		}
		if res.Observation, err = getObservationByID(ctx, tx, id); err != nil {
			return err
		}
		if !res.Deduped {
			cands, err := findCandidates(ctx, tx, id)
			if err != nil {
				return err
			}
			res.Candidates, err = createPendingRelations(ctx, tx, now, in.SessionID, res.Observation.SyncID, cands)
			return err
		}
		return nil
	})
	return res, err
}

func (s *Store) writeObservation(ctx context.Context, tx *sql.Tx, in NewObservation, hash, now string, res *SaveResult) (int64, error) {
	var id int64
	if in.TopicKey != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM observations
			WHERE topic_key = ? AND project = ? AND scope = ? AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC LIMIT 1`, in.TopicKey, in.Project, in.Scope).Scan(&id)
		switch {
		case err == nil:
			res.Upserted = true
			_, err = tx.ExecContext(ctx, `UPDATE observations SET
				  type = ?, title = ?, content = ?, tool_name = ?, task_id = ?, kind = ?, normalized_hash = ?,
				  revision_count = revision_count + 1, last_seen_at = ?, updated_at = ?
				WHERE id = ?`, in.Type, in.Title, in.Content, nullable(in.ToolName), nullable(in.TaskID),
				nullable(in.Kind), hash, now, now, id)
			return id, err
		case err != sql.ErrNoRows:
			return 0, err
		}
	} else {
		cutoff := s.now().Add(-DedupeWindow).UTC().Format(TimeLayout)
		err := tx.QueryRowContext(ctx, `SELECT id FROM observations
			WHERE normalized_hash = ? AND project = ? AND scope = ? AND type = ? AND title = ?
			  AND created_at >= ? AND deleted_at IS NULL
			ORDER BY created_at DESC, id DESC LIMIT 1`, hash, in.Project, in.Scope, in.Type, in.Title, cutoff).Scan(&id)
		switch {
		case err == nil:
			res.Deduped = true
			_, err = tx.ExecContext(ctx, `UPDATE observations SET
				  duplicate_count = duplicate_count + 1, last_seen_at = ?, updated_at = ?
				WHERE id = ?`, now, now, id)
			return id, err
		case err != sql.ErrNoRows:
			return 0, err
		}
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO observations
		  (sync_id, session_id, type, title, content, project, scope, topic_key, tool_name, task_id, kind,
		   normalized_hash, last_seen_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newSyncID("obs-"), in.SessionID, in.Type, in.Title, in.Content, in.Project, in.Scope,
		nullable(in.TopicKey), nullable(in.ToolName), nullable(in.TaskID), nullable(in.Kind),
		hash, now, now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// ObservationPatch is the input of UpdateObservation. A nil field is left
// untouched; a pointer to "" clears a nullable column.
type ObservationPatch struct {
	Title    *string
	Content  *string
	Type     *string
	TopicKey *string
	TaskID   *string
	Scope    *string
	Kind     *string
}

// UpdateObservation applies the patch, bumps revision_count and returns the
// row together with the names of the columns it changed.
func (s *Store) UpdateObservation(ctx context.Context, syncID string, p ObservationPatch) (Observation, []string, error) {
	var sets []string
	var args []any
	var changed []string
	set := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
		changed = append(changed, col)
	}
	if p.Title != nil {
		if *p.Title == "" {
			return Observation{}, nil, errorf(ErrInvalidRequest, "title cannot be empty")
		}
		set("title", *p.Title)
	}
	if p.Content != nil {
		if *p.Content == "" {
			return Observation{}, nil, errorf(ErrInvalidRequest, "content cannot be empty")
		}
		set("content", *p.Content)
		sets = append(sets, "normalized_hash = ?")
		args = append(args, NormalizedHash(*p.Content))
	}
	if p.Type != nil {
		if !slices.Contains(ObservationTypes, *p.Type) {
			return Observation{}, nil, errorf(ErrInvalidType, "type %q is not one of %s", *p.Type, strings.Join(ObservationTypes, ", "))
		}
		set("type", *p.Type)
	}
	if p.TopicKey != nil {
		set("topic_key", nullable(*p.TopicKey))
	}
	if p.TaskID != nil {
		set("task_id", nullable(*p.TaskID))
	}
	if p.Scope != nil {
		if *p.Scope != ScopeProject && *p.Scope != ScopePersonal {
			return Observation{}, nil, errorf(ErrInvalidEnum, "scope must be project or personal")
		}
		set("scope", *p.Scope)
	}
	if p.Kind != nil {
		if *p.Kind != "" && *p.Kind != KindHandoff {
			return Observation{}, nil, errorf(ErrInvalidEnum, "kind must be handoff or null")
		}
		set("kind", nullable(*p.Kind))
	}
	if len(sets) == 0 {
		return Observation{}, nil, errorf(ErrInvalidRequest, "nothing to update besides sync_id")
	}
	var obs Observation
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		query := `UPDATE observations SET ` + strings.Join(sets, ", ") +
			`, revision_count = revision_count + 1, updated_at = ? WHERE sync_id = ? AND deleted_at IS NULL`
		res, err := tx.ExecContext(ctx, query, append(args, s.timestamp(), syncID)...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errorf(ErrNotFound, "observation %q does not exist", syncID)
		}
		obs, err = getObservation(ctx, tx, `o.sync_id = ?`, syncID)
		return err
	})
	if err != nil {
		return Observation{}, nil, err
	}
	return obs, changed, nil
}

// DeleteObservation is the soft delete: it stamps deleted_at, which hides the
// row from every read.
func (s *Store) DeleteObservation(ctx context.Context, syncID string) error {
	now := s.timestamp()
	res, err := s.db.ExecContext(ctx, `UPDATE observations SET deleted_at = ?, updated_at = ?
		WHERE sync_id = ? AND deleted_at IS NULL`, now, now, syncID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errorf(ErrNotFound, "observation %q does not exist", syncID)
	}
	return nil
}

func (s *Store) GetObservation(ctx context.Context, syncID string) (Observation, error) {
	return getObservation(ctx, s.db, `o.sync_id = ?`, syncID)
}

func validateObservation(typ, scope, kind string) error {
	if !slices.Contains(ObservationTypes, typ) {
		return errorf(ErrInvalidType, "type %q is not one of %s", typ, strings.Join(ObservationTypes, ", "))
	}
	if scope != ScopeProject && scope != ScopePersonal {
		return errorf(ErrInvalidEnum, "scope must be project or personal")
	}
	if kind != "" && kind != KindHandoff {
		return errorf(ErrInvalidEnum, "kind must be handoff or null")
	}
	return nil
}

const observationColumns = `o.sync_id, o.session_id, o.type, o.title, o.content, o.project, o.scope,
	o.topic_key, o.tool_name, o.task_id, o.kind, o.normalized_hash, o.revision_count, o.duplicate_count,
	o.last_seen_at, o.review_after, o.pinned, o.created_at, o.updated_at`

func getObservationByID(ctx context.Context, q querier, id int64) (Observation, error) {
	return getObservation(ctx, q, `o.id = ?`, id)
}

func getObservation(ctx context.Context, q querier, where string, arg any) (Observation, error) {
	row := q.QueryRowContext(ctx, `SELECT `+observationColumns+` FROM observations o
		WHERE `+where+` AND o.deleted_at IS NULL`, arg)
	obs, err := scanObservation(row.Scan)
	if err == sql.ErrNoRows {
		return Observation{}, errorf(ErrNotFound, "observation %v does not exist", arg)
	}
	return obs, err
}

func scanObservation(scan func(...any) error, extra ...any) (Observation, error) {
	var o Observation
	var topicKey, toolName, taskID, kind, hash, lastSeen, reviewAfter sql.NullString
	dest := append([]any{&o.SyncID, &o.SessionID, &o.Type, &o.Title, &o.Content, &o.Project, &o.Scope,
		&topicKey, &toolName, &taskID, &kind, &hash, &o.RevisionCount, &o.DuplicateCount,
		&lastSeen, &reviewAfter, &o.Pinned, &o.CreatedAt, &o.UpdatedAt}, extra...)
	if err := scan(dest...); err != nil {
		return Observation{}, err
	}
	o.TopicKey, o.ToolName, o.TaskID, o.Kind = topicKey.String, toolName.String, taskID.String, kind.String
	o.NormalizedHash, o.LastSeenAt, o.ReviewAfter = hash.String, lastSeen.String, reviewAfter.String
	return o, nil
}
