package store

import (
	"context"
	"database/sql"
	"path/filepath"
)

// NewWorktree is the input of UpsertWorktree. Task is a key or id; empty keeps
// the stored task, or none on insert.
type NewWorktree struct {
	Path      string
	Repo      string
	Branch    string
	Base      string
	Purpose   string
	Task      string
	SessionID string
}

// WorktreeRegistered is the body of worktree_register in docs/specs/api.md.
type WorktreeRegistered struct {
	Worktree
	Created bool `json:"created"`
}

// UpsertWorktree registers by absolute path. On an existing path it rewrites
// repo, branch, base and purpose and keeps the creator and the creation time.
func (s *Store) UpsertWorktree(ctx context.Context, in NewWorktree) (WorktreeRegistered, error) {
	if in.Path == "" || in.Repo == "" || in.Branch == "" {
		return WorktreeRegistered{}, errorf(ErrInvalidRequest, "path, repo and branch are required")
	}
	if !filepath.IsAbs(in.Path) {
		return WorktreeRegistered{}, errorf(ErrInvalidRequest, "path %q must be absolute", in.Path)
	}
	path := filepath.Clean(in.Path)

	var out WorktreeRegistered
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var taskID sql.NullString
		if in.Task != "" {
			id, err := lookupTask(ctx, tx, in.Task)
			if err != nil {
				return err
			}
			taskID = sql.NullString{String: id, Valid: true}
		}
		now := s.timestamp()
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM worktrees WHERE path = ?`, path).Scan(&id)
		switch {
		case err == sql.ErrNoRows:
			out.Created = true
			_, err = tx.ExecContext(ctx, `INSERT INTO worktrees (id, task_id, path, repo, branch, base, purpose,
				  created_by_session, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				NewID(s.now()), taskID, path, in.Repo, in.Branch, nullable(in.Base), nullable(in.Purpose),
				nullable(in.SessionID), now, now)
		case err == nil:
			_, err = tx.ExecContext(ctx, `UPDATE worktrees SET task_id = COALESCE(?, task_id), repo = ?, branch = ?,
				  base = ?, purpose = ?, updated_at = ? WHERE id = ?`,
				taskID, in.Repo, in.Branch, nullable(in.Base), nullable(in.Purpose), now, id)
		}
		if err != nil {
			return err
		}
		rows, err := listWorktrees(ctx, tx, "w.path = ?", []any{path})
		if err != nil {
			return err
		}
		out.Worktree = rows[0]
		return nil
	})
	return out, err
}
