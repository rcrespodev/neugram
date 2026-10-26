package store

import "context"

var ErrInvalidField = &Error{Code: "invalid_field"}

var observationFixedColumns = []string{
	"id", "sync_id", "session_id", "type", "title", "content", "project", "scope", "topic_key",
	"tool_name", "task_id", "kind", "normalized_hash", "revision_count", "duplicate_count",
	"last_seen_at", "review_after", "pinned", "created_at", "updated_at", "deleted_at",
}

// taskScope returns the task id plus, on request, its ancestors and its subtree.
func (s *Store) taskScope(ctx context.Context, ref string, ancestors, subtree bool) ([]string, error) {
	id, err := s.resolveTask(ctx, ref)
	if err != nil {
		return nil, err
	}
	ids := []string{id}
	collect := func(query string) error {
		rows, err := s.db.QueryContext(ctx, query, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return err
			}
			ids = append(ids, v)
		}
		return rows.Err()
	}
	if ancestors {
		if err := collect(`WITH RECURSIVE up(id) AS (
			SELECT parent_task_id FROM tasks WHERE id = ? AND parent_task_id IS NOT NULL
			UNION SELECT t.parent_task_id FROM tasks t JOIN up ON t.id = up.id WHERE t.parent_task_id IS NOT NULL
		) SELECT id FROM up`); err != nil {
			return nil, err
		}
	}
	if subtree {
		if err := collect(`WITH RECURSIVE down(id) AS (
			SELECT id FROM tasks WHERE parent_task_id = ?
			UNION SELECT t.id FROM tasks t JOIN down ON t.parent_task_id = down.id
		) SELECT id FROM down`); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
