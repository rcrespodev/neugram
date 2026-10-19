package store

import (
	"context"
	"database/sql"
)

type Session struct {
	ID        string
	Project   string
	Directory string
	StartedAt string
	EndedAt   string
	Summary   string
	TaskID    string
	PinnedAt  string
	PinnedBy  string
	CreatedAt string
	UpdatedAt string
}

// UpsertSession inserts the session row only when it does not exist yet; an
// existing row, its project, directory and pin, is returned untouched.
// created reports whether the row is new.
func (s *Store) UpsertSession(ctx context.Context, id, project, directory string) (sess Session, created bool, err error) {
	if id == "" || project == "" || directory == "" {
		return Session{}, false, errorf(ErrInvalidRequest, "id, project and directory are required")
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		now := s.timestamp()
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, project, directory, started_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`, id, project, directory, now, now, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		created = n == 1
		sess, err = getSession(ctx, tx, id)
		return err
	})
	return sess, created, err
}

func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	return getSession(ctx, s.db, id)
}

// EndSession stamps ended_at.
func (s *Store) EndSession(ctx context.Context, id string) error {
	now := s.timestamp()
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET ended_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errorf(ErrUnknownSession, "session %q does not exist", id)
	}
	return nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func getSession(ctx context.Context, q querier, id string) (Session, error) {
	var ss Session
	var endedAt, summary, taskID, pinnedAt, pinnedBy sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, project, directory, started_at, ended_at, summary,
		  task_id, pinned_at, pinned_by, created_at, updated_at
		FROM sessions WHERE id = ?`, id).Scan(&ss.ID, &ss.Project, &ss.Directory, &ss.StartedAt,
		&endedAt, &summary, &taskID, &pinnedAt, &pinnedBy, &ss.CreatedAt, &ss.UpdatedAt)
	if err == sql.ErrNoRows {
		return Session{}, errorf(ErrUnknownSession, "session %q does not exist", id)
	}
	ss.EndedAt, ss.Summary, ss.TaskID = endedAt.String, summary.String, taskID.String
	ss.PinnedAt, ss.PinnedBy = pinnedAt.String, pinnedBy.String
	return ss, err
}
