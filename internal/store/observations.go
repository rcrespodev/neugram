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

// NewObservation is the input of AddObservation. TaskID is a task key or id,
// "none" for no task, or empty to use the session's pin; Project defaults to
// the session's.
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
	Observation     Observation
	Deduped         bool
	Upserted        bool
	Candidates      []Candidate
	TaskKey         string
	FooterRewritten bool
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
	if in.SessionID == "" || in.Title == "" || in.Content == "" {
		return SaveResult{}, errorf(ErrInvalidRequest, "session_id, title and content are required")
	}
	var res SaveResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		sess, err := getSession(ctx, tx, in.SessionID)
		if err != nil {
			return err
		}
		if in.Project == "" {
			in.Project = sess.Project
		}
		task, err := resolveSaveTask(ctx, tx, sess, in.TaskID)
		if err != nil {
			return err
		}
		in.TaskID = task.id
		res.TaskKey = task.key
		in.Content, res.FooterRewritten = withTaskFooter(in.Content, task.key, task.title)
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

type saveTask struct{ id, key, title string }

// resolveSaveTask applies the task rules of mem_save: the explicit argument,
// else the session's pin; no argument and no pin is no_pin. A none pin or
// argument yields the zero saveTask.
func resolveSaveTask(ctx context.Context, q querier, sess Session, ref string) (saveTask, error) {
	switch {
	case ref == "" && sess.PinnedAt == "":
		return saveTask{}, noPinError(ctx, q)
	case strings.EqualFold(ref, NoTaskKey), ref == "" && sess.TaskID == "":
		return saveTask{}, nil
	case ref == "":
		ref = sess.TaskID
	}
	id, err := lookupTask(ctx, q, ref)
	if err != nil {
		return saveTask{}, err
	}
	card, err := taskCard(ctx, q, id)
	if err != nil {
		return saveTask{}, err
	}
	return saveTask{id: id, key: card.TaskKey, title: card.Title}, nil
}

// hasUnresolvableFooter reports a footer that names no task, such as an
// imported one; an update that leaves the task alone must not erase it.
func hasUnresolvableFooter(ctx context.Context, q querier, content string) bool {
	_, footer, found := splitFooter(content)
	if !found || footerMatches(footer, "") {
		return false
	}
	_, err := lookupTask(ctx, q, footerKey(footer))
	return isCode(err, ErrUnknownTask)
}

func noPinError(ctx context.Context, q querier) error {
	open := []string{}
	rows, err := q.QueryContext(ctx, `SELECT key, title FROM tasks WHERE status != ? ORDER BY updated_at DESC, key`, StatusDone)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, title string
		if err := rows.Scan(&key, &title); err != nil {
			return err
		}
		open = append(open, key+" — "+title)
	}
	return &Error{Code: ErrNoPin.Code, Message: "the session has no pinned task and the call carries no task_id",
		Hint: `task_pin {"task_id": "<key>"} or task_pin {"task_id": "none"}`, Details: map[string]any{"open_tasks": open}}
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

// UpdateResult is what UpdateObservation returns: the row, the names of the
// columns it changed and whether an existing `**Task**` footer was corrected.
type UpdateResult struct {
	Observation     Observation
	Changed         []string
	TaskKey         string
	FooterRewritten bool
}

// UpdateObservation applies the patch and bumps revision_count. Changing the
// task or the content re-applies the `**Task**` footer, and a footer change
// shows up as "content" in Changed even when the patch did not send content.
func (s *Store) UpdateObservation(ctx context.Context, syncID string, p ObservationPatch) (UpdateResult, error) {
	var sets []string
	var args []any
	var changed []string
	set := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
		if !slices.Contains(changed, col) {
			changed = append(changed, col)
		}
	}
	if p.Title != nil {
		if *p.Title == "" {
			return UpdateResult{}, errorf(ErrInvalidRequest, "title cannot be empty")
		}
		set("title", *p.Title)
	}
	if p.Content != nil && *p.Content == "" {
		return UpdateResult{}, errorf(ErrInvalidRequest, "content cannot be empty")
	}
	if p.Type != nil {
		if !slices.Contains(ObservationTypes, *p.Type) {
			return UpdateResult{}, errorf(ErrInvalidType, "type %q is not one of %s", *p.Type, strings.Join(ObservationTypes, ", "))
		}
		set("type", *p.Type)
	}
	if p.TopicKey != nil {
		set("topic_key", nullable(*p.TopicKey))
	}
	if p.Scope != nil {
		if *p.Scope != ScopeProject && *p.Scope != ScopePersonal {
			return UpdateResult{}, errorf(ErrInvalidEnum, "scope must be project or personal")
		}
		set("scope", *p.Scope)
	}
	if p.Kind != nil {
		if *p.Kind != "" && *p.Kind != KindHandoff {
			return UpdateResult{}, errorf(ErrInvalidEnum, "kind must be handoff or null")
		}
		set("kind", nullable(*p.Kind))
	}
	if len(sets) == 0 && p.Content == nil && p.TaskID == nil {
		return UpdateResult{}, errorf(ErrInvalidRequest, "nothing to update besides sync_id")
	}
	var res UpdateResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		cur, err := getObservation(ctx, tx, `o.sync_id = ?`, syncID)
		if err != nil {
			return err
		}
		sets, args := sets, args
		if p.Content != nil || p.TaskID != nil {
			ref := cur.TaskID
			if p.TaskID != nil {
				ref = *p.TaskID
			}
			if ref == "" {
				ref = NoTaskKey
			}
			task, err := resolveSaveTask(ctx, tx, Session{PinnedAt: "-"}, ref)
			if err != nil {
				return err
			}
			if p.TaskID != nil && task.id != cur.TaskID {
				sets, args = append(sets, "task_id = ?"), append(args, nullable(task.id))
				changed = append(changed, "task_id")
			}
			res.TaskKey = task.key
			content := cur.Content
			if p.Content != nil {
				content = *p.Content
			}
			out, rewritten := content, false
			if p.TaskID != nil || !hasUnresolvableFooter(ctx, tx, content) {
				out, rewritten = withTaskFooter(content, task.key, task.title)
			}
			res.FooterRewritten = rewritten
			if out != cur.Content {
				sets = append(sets, "content = ?", "normalized_hash = ?")
				args = append(args, out, NormalizedHash(out))
				if !slices.Contains(changed, "content") {
					changed = append(changed, "content")
				}
			}
		}
		query := `UPDATE observations SET ` + strings.Join(sets, ", ") +
			`, revision_count = revision_count + 1, updated_at = ? WHERE sync_id = ? AND deleted_at IS NULL`
		if _, err := tx.ExecContext(ctx, query, append(args, s.timestamp(), syncID)...); err != nil {
			return err
		}
		res.Observation, err = getObservation(ctx, tx, `o.sync_id = ?`, syncID)
		return err
	})
	res.Changed = changed
	if err != nil {
		return UpdateResult{}, err
	}
	return res, nil
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
