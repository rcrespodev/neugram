package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

var pinners = []string{"agent", "hook", "cli", "import"}

const defaultPinner = "agent"

// NewTask is the input of CreateTask. Product is a name; Parent a task key or id.
type NewTask struct {
	Key     string
	Title   string
	Product string
	Parent  string
	Tracker *Tracker
	Notes   string
}

// CreateTask inserts a task in status todo. Without Product it copies the
// parent's, once. Depth limits are enforced by the DDL triggers.
func (s *Store) CreateTask(ctx context.Context, in NewTask) (TaskCard, error) {
	var card TaskCard
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		id, err := s.insertTask(ctx, tx, in)
		if err != nil {
			return err
		}
		card, err = taskCard(ctx, tx, id)
		return err
	})
	return card, err
}

func (s *Store) insertTask(ctx context.Context, tx *sql.Tx, in NewTask) (string, error) {
	if in.Key == "" || in.Title == "" {
		return "", errorf(ErrInvalidRequest, "key and title are required")
	}
	if strings.EqualFold(in.Key, NoTaskKey) {
		return "", errorf(ErrInvalidRequest, "%q is reserved for the no-task pin", in.Key)
	}
	if _, err := lookupTask(ctx, tx, in.Key); err == nil {
		return "", errorf(ErrTaskExists, "task %q already exists", in.Key)
	} else if !isCode(err, ErrUnknownTask) {
		return "", err
	}

	var productID, parentID sql.NullString
	if in.Parent != "" {
		id, err := lookupTask(ctx, tx, in.Parent)
		if err != nil {
			if isCode(err, ErrUnknownTask) {
				return "", errorf(ErrInvalidParent, "parent %q does not exist", in.Parent)
			}
			return "", err
		}
		parentID = sql.NullString{String: id, Valid: true}
		if err := tx.QueryRowContext(ctx, `SELECT product_id FROM tasks WHERE id = ?`, id).Scan(&productID); err != nil {
			return "", err
		}
	}
	if in.Product != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM products WHERE name = ?`, in.Product).Scan(&productID.String)
		if err == sql.ErrNoRows {
			return "", errorf(ErrUnknownProduct, "product %q does not exist", in.Product)
		}
		if err != nil {
			return "", err
		}
		productID.Valid = true
	}

	var kind, trackerID, trackerURL any
	if in.Tracker != nil {
		kind, trackerID, trackerURL = nullable(in.Tracker.Kind), nullable(in.Tracker.ID), nullable(in.Tracker.URL)
	}
	id, now := NewID(s.now()), s.timestamp()
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, key, title, product_id, parent_task_id, status,
		  tracker_kind, tracker_id, tracker_url, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'todo', ?, ?, ?, ?, ?, ?)`,
		id, in.Key, in.Title, productID, parentID, kind, trackerID, trackerURL, nullable(in.Notes), now, now)
	if err != nil && strings.Contains(err.Error(), "parent_task_id:") {
		return "", errorf(ErrInvalidParent, "%s", err.Error())
	}
	return id, err
}

// PinRequest is the input of PinSession. Target is a task key or id, or "none".
// When Target names no task and Title is set, the task is created from the
// remaining fields.
type PinRequest struct {
	SessionID string
	Target    string
	PinnedBy  string
	Title     string
	Product   string
	Parent    string
	Tracker   *Tracker
	Notes     string
}

// PinResult is the body of task_pin in docs/specs/api.md. A pin to none has a
// nil TaskID and TaskKey "none".
type PinResult struct {
	SessionID    string   `json:"session_id"`
	TaskID       *string  `json:"task_id"`
	TaskKey      string   `json:"task_key"`
	Title        string   `json:"title,omitempty"`
	Status       string   `json:"status,omitempty"`
	Product      *string  `json:"product"`
	ParentKey    *string  `json:"parent_key"`
	AncestorKeys []string `json:"ancestor_keys"`
	Created      bool     `json:"created"`
	RepinnedFrom *string  `json:"repinned_from"`
	PinnedAt     string   `json:"pinned_at"`
	PinnedBy     string   `json:"pinned_by"`
}

// PinSession sets the session's task. Creating the task and pinning it are one
// transaction, so a failed pin leaves no task behind.
func (s *Store) PinSession(ctx context.Context, in PinRequest) (PinResult, error) {
	if in.Target == "" {
		return PinResult{}, errorf(ErrInvalidRequest, "task_id is required")
	}
	if in.PinnedBy == "" {
		in.PinnedBy = defaultPinner
	}
	if !slices.Contains(pinners, in.PinnedBy) {
		return PinResult{}, errorf(ErrInvalidRequest, "pinned_by %q: allowed %s", in.PinnedBy, strings.Join(pinners, ", "))
	}
	var res PinResult
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		sess, err := getSession(ctx, tx, in.SessionID)
		if err != nil {
			return err
		}
		res = PinResult{SessionID: sess.ID, AncestorKeys: []string{}, PinnedBy: in.PinnedBy, PinnedAt: s.timestamp()}

		var taskID string
		if strings.EqualFold(in.Target, NoTaskKey) {
			res.TaskKey = NoTaskKey
		} else {
			taskID, err = lookupTask(ctx, tx, in.Target)
			if isCode(err, ErrUnknownTask) && in.Title != "" {
				taskID, err = s.insertTask(ctx, tx, NewTask{Key: in.Target, Title: in.Title, Product: in.Product,
					Parent: in.Parent, Tracker: in.Tracker, Notes: in.Notes})
				res.Created = err == nil
			}
			if err != nil {
				return err
			}
			card, err := taskCard(ctx, tx, taskID)
			if err != nil {
				return err
			}
			res.TaskID, res.TaskKey, res.Title, res.Status = &card.TaskID, card.TaskKey, card.Title, card.Status
			res.Product, res.ParentKey, res.AncestorKeys = card.Product, card.ParentKey, card.AncestorKeys
		}

		if sess.PinnedAt != "" && sess.TaskID != taskID {
			prev := NoTaskKey
			if sess.TaskID != "" {
				if err := tx.QueryRowContext(ctx, `SELECT key FROM tasks WHERE id = ?`, sess.TaskID).Scan(&prev); err != nil {
					return err
				}
			}
			res.RepinnedFrom = &prev
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET task_id = ?, pinned_at = ?, pinned_by = ?, updated_at = ? WHERE id = ?`,
			nullable(taskID), res.PinnedAt, res.PinnedBy, res.PinnedAt, sess.ID)
		return err
	})
	if err != nil {
		return PinResult{}, err
	}
	return res, nil
}
