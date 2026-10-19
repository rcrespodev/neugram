package store

import (
	"context"
	"database/sql"
	"strings"
)

const DefaultPromptLimit = 50

type Prompt struct {
	SyncID    string
	SessionID string
	Content   string
	Project   string
	CreatedAt string
}

// AddPrompt stores a user prompt. The same content already saved in the same
// session is returned with deduped set instead of being inserted again. An
// empty project defaults to the session's.
func (s *Store) AddPrompt(ctx context.Context, sessionID, content, project string) (p Prompt, deduped bool, err error) {
	if content == "" {
		return Prompt{}, false, errorf(ErrInvalidRequest, "content is required")
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		sess, err := getSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if project == "" {
			project = sess.Project
		}
		err = tx.QueryRowContext(ctx, `SELECT sync_id, session_id, content, project, created_at FROM user_prompts
			WHERE session_id = ? AND content = ? ORDER BY id DESC LIMIT 1`, sessionID, content).
			Scan(&p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt)
		if err == nil {
			deduped = true
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		p = Prompt{SyncID: newSyncID("prm-"), SessionID: sessionID, Content: content, Project: project, CreatedAt: s.timestamp()}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_prompts (sync_id, session_id, content, project, created_at)
			VALUES (?, ?, ?, ?, ?)`, p.SyncID, p.SessionID, p.Content, p.Project, p.CreatedAt)
		return err
	})
	return p, deduped, err
}

type PromptQuery struct {
	SessionID string
	Project   string
	Query     string
	Limit     int
	Offset    int
}

// ListPrompts filters prompts by session and project, optionally through
// prompts_fts with the same literal query rules as observations.
func (s *Store) ListPrompts(ctx context.Context, q PromptQuery) ([]Prompt, int, error) {
	if q.Limit == 0 {
		q.Limit = DefaultPromptLimit
	}
	if q.Limit < 0 || q.Limit > MaxSearchLimit || q.Offset < 0 {
		return nil, 0, errorf(ErrInvalidRequest, "limit must be 1..%d and offset >= 0", MaxSearchLimit)
	}
	if q.SessionID != "" {
		if _, err := getSession(ctx, s.db, q.SessionID); err != nil {
			return nil, 0, err
		}
	}
	from, order := `FROM user_prompts p`, `p.created_at DESC, p.id DESC`
	var where []string
	var args []any
	if strings.TrimSpace(q.Query) != "" {
		match, err := promptMatch(q.Query)
		if err != nil {
			return nil, 0, err
		}
		from, order = `FROM prompts_fts f JOIN user_prompts p ON p.id = f.rowid`, `f.rank`
		where = append(where, "prompts_fts MATCH ?")
		args = append(args, match)
	}
	if q.SessionID != "" {
		where = append(where, "p.session_id = ?")
		args = append(args, q.SessionID)
	}
	if q.Project != "" {
		where = append(where, "p.project = ?")
		args = append(args, q.Project)
	}
	cond := from
	if len(where) > 0 {
		cond += ` WHERE ` + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.sync_id, p.session_id, p.content, p.project, p.created_at `+
		cond+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.SyncID, &p.SessionID, &p.Content, &p.Project, &p.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
