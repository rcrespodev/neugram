package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

// StatusChange is the body of task_status in docs/specs/api.md.
type StatusChange struct {
	TaskID         string   `json:"task_id"`
	TaskKey        string   `json:"task_key"`
	Status         string   `json:"status"`
	PreviousStatus string   `json:"previous_status"`
	ParentKey      *string  `json:"parent_key"`
	AncestorKeys   []string `json:"ancestor_keys"`
	OpenChildren   int      `json:"open_children"`
	UpdatedAt      string   `json:"updated_at"`
}

// SetTaskStatus applies the change even when the task is closed with open
// direct children; their count travels in OpenChildren as a warning.
func (s *Store) SetTaskStatus(ctx context.Context, ref, status string) (StatusChange, error) {
	if !slices.Contains(TaskStatuses, status) {
		return StatusChange{}, errorf(ErrInvalidEnum, "status %q: allowed %s", status, strings.Join(TaskStatuses, ", "))
	}
	var out StatusChange
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		id, err := lookupTask(ctx, tx, ref)
		if err != nil {
			return err
		}
		card, err := taskCard(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.timestamp()
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, status, now, id); err != nil {
			return err
		}
		out = StatusChange{TaskID: id, TaskKey: card.TaskKey, Status: status, PreviousStatus: card.Status,
			ParentKey: card.ParentKey, AncestorKeys: card.AncestorKeys, UpdatedAt: now}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE parent_task_id = ? AND status != ?`,
			id, StatusDone).Scan(&out.OpenChildren)
	})
	return out, err
}
