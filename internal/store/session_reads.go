package store

import (
	"context"
	"database/sql"
)

// HandoffNote is a handoff observation with the key of its task, "none" when it has none.
type HandoffNote struct {
	Observation Observation
	TaskKey     string
}

// LastHandoff is the newest handoff of the task itself, never of an ancestor.
func (s *Store) LastHandoff(ctx context.Context, taskRef string) (HandoffNote, error) {
	id, err := s.resolveTask(ctx, taskRef)
	if err != nil {
		return HandoffNote{}, err
	}
	return s.lastHandoff(ctx, `o.task_id = ?`, id)
}

// LastHandoffByDirectory is the newest handoff of any session started in dir.
func (s *Store) LastHandoffByDirectory(ctx context.Context, dir string) (HandoffNote, error) {
	return s.lastHandoff(ctx, `o.session_id IN (SELECT id FROM sessions WHERE directory = ?)`, dir)
}

func (s *Store) lastHandoff(ctx context.Context, cond string, arg any) (HandoffNote, error) {
	var note HandoffNote
	var key sql.NullString
	row := s.db.QueryRowContext(ctx, `SELECT `+observationColumns+`, t.key
		FROM observations o LEFT JOIN tasks t ON t.id = o.task_id
		WHERE `+cond+` AND o.kind = ? AND o.deleted_at IS NULL
		ORDER BY o.created_at DESC, o.id DESC LIMIT 1`, arg, KindHandoff)
	obs, err := scanObservation(row.Scan, &key)
	if err == sql.ErrNoRows {
		return note, errorf(ErrNotFound, "no handoff found")
	}
	if err != nil {
		return note, err
	}
	note.Observation, note.TaskKey = obs, NoTaskKey
	if key.Valid {
		note.TaskKey = key.String
	}
	return note, nil
}

// PinHint is a pinned session with its task key, "none" for a pin to none.
type PinHint struct {
	Session Session
	TaskKey string
}

// LastPinnedSessionByDirectory is the most recently pinned session started in
// dir, excluding exceptID, for the hint after /clear.
func (s *Store) LastPinnedSessionByDirectory(ctx context.Context, dir, exceptID string) (PinHint, error) {
	var hint PinHint
	var key sql.NullString
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT s.id, t.key FROM sessions s LEFT JOIN tasks t ON t.id = s.task_id
		WHERE s.directory = ? AND s.id != ? AND s.pinned_at IS NOT NULL
		ORDER BY s.pinned_at DESC, s.created_at DESC LIMIT 1`, dir, exceptID).Scan(&id, &key)
	if err == sql.ErrNoRows {
		return hint, errorf(ErrNotFound, "no pinned session in %q", dir)
	}
	if err != nil {
		return hint, err
	}
	if hint.Session, err = getSession(ctx, s.db, id); err != nil {
		return hint, err
	}
	hint.TaskKey = NoTaskKey
	if key.Valid {
		hint.TaskKey = key.String
	}
	return hint, nil
}
