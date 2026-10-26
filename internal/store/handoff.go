package store

import (
	"context"
	"database/sql"
	"strings"
)

// Handoff is the input of SaveHandoff. TaskID is a task key or id, "none", or
// empty to use the session's pin; an empty Title is derived from the content.
type Handoff struct {
	SessionID string
	TaskID    string
	Title     string
	Content   string
}

type HandoffResult struct {
	Observation     Observation
	TaskKey         string
	Replaced        bool
	FooterRewritten bool
}

// SaveHandoff keeps one handoff per session: a second call overwrites the
// first row instead of adding another. The task is fixed at each write.
func (s *Store) SaveHandoff(ctx context.Context, in Handoff) (HandoffResult, error) {
	if in.SessionID == "" || in.Content == "" {
		return HandoffResult{}, errorf(ErrInvalidRequest, "session_id and content are required")
	}
	var res HandoffResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		sess, err := getSession(ctx, tx, in.SessionID)
		if err != nil {
			return err
		}
		task, err := resolveSaveTask(ctx, tx, sess, in.TaskID)
		if err != nil {
			return err
		}
		res.TaskKey = task.key
		if res.TaskKey == "" {
			res.TaskKey = NoTaskKey
		}
		content, rewritten := withTaskFooter(in.Content, task.key, task.title)
		res.FooterRewritten = rewritten
		title := in.Title
		if title == "" {
			title = "Handoff " + res.TaskKey + ": " + handoffHeadline(content)
		}
		hash, now := NormalizedHash(content), s.timestamp()

		var id int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM observations
			WHERE session_id = ? AND kind = ? AND deleted_at IS NULL
			ORDER BY id LIMIT 1`, in.SessionID, KindHandoff).Scan(&id)
		switch err {
		case nil:
			res.Replaced = true
			_, err = tx.ExecContext(ctx, `UPDATE observations SET
				  title = ?, content = ?, task_id = ?, normalized_hash = ?,
				  revision_count = revision_count + 1, last_seen_at = ?, updated_at = ?
				WHERE id = ?`, title, content, nullable(task.id), hash, now, now, id)
		case sql.ErrNoRows:
			var r sql.Result
			r, err = tx.ExecContext(ctx, `INSERT INTO observations
				  (sync_id, session_id, type, title, content, project, scope, task_id, kind,
				   normalized_hash, last_seen_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				newSyncID("obs-"), in.SessionID, typeHandoff, title, content, sess.Project, ScopeProject,
				nullable(task.id), KindHandoff, hash, now, now, now)
			if err == nil {
				id, err = r.LastInsertId()
			}
		}
		if err != nil {
			return err
		}
		res.Observation, err = getObservationByID(ctx, tx, id)
		return err
	})
	return res, err
}

// handoffHeadline is the first body line under the "State" heading, or else
// the first line that is not a heading.
func handoffHeadline(content string) string {
	var first string
	afterState := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
			afterState = strings.EqualFold(strings.TrimSpace(strings.TrimLeft(line, "#")), "state")
		case strings.HasPrefix(line, footerPrefix):
		case afterState:
			return line
		case first == "":
			first = line
		}
	}
	return first
}
