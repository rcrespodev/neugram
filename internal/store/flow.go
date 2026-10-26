package store

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
)

var TaskStatuses = []string{"todo", "in-progress", "review", "blocked", "done"}

const (
	StatusDone   = "done"
	NoTaskKey    = "none"
	DefaultLimit = 50
	MaxLimit     = 200

	DefaultObservationsLimit = 10
	MaxObservationsLimit     = 100
	observationPreviewLen    = 300
)

var ErrUnknownTask = &Error{Code: "unknown_task"}

type Tracker struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	URL  string `json:"url"`
}

// TaskSummary is one row of GET /tasks; its shape is task_list in docs/specs/api.md.
type TaskSummary struct {
	TaskID            string   `json:"task_id"`
	TaskKey           string   `json:"task_key"`
	Title             string   `json:"title"`
	Status            string   `json:"status"`
	Product           *string  `json:"product"`
	ParentKey         *string  `json:"parent_key"`
	AncestorKeys      []string `json:"ancestor_keys"`
	Tracker           *Tracker `json:"tracker"`
	SessionsCount     int      `json:"sessions_count"`
	WorktreesAlive    int      `json:"worktrees_alive"`
	ObservationsCount int      `json:"observations_count"`
	ChildrenCount     int      `json:"children_count"`
	LastObservationAt *string  `json:"last_observation_at"`
}

type TaskQuery struct {
	Statuses    []string
	Product     string
	Parent      string
	RootsOnly   bool
	IncludeDone bool
	Limit       int
	Offset      int
}

// ListTasks returns the tasks ordered by key, byte-wise, and the total before
// paging. Without Statuses or IncludeDone, done tasks are left out.
func (s *Store) ListTasks(ctx context.Context, q TaskQuery) ([]TaskSummary, int, error) {
	if err := checkPage(&q.Limit, q.Offset, DefaultLimit); err != nil {
		return nil, 0, err
	}
	var where []string
	var args []any
	if len(q.Statuses) > 0 {
		for _, st := range q.Statuses {
			if !slices.Contains(TaskStatuses, st) {
				return nil, 0, errorf(ErrInvalidEnum, "status %q: allowed %s", st, strings.Join(TaskStatuses, ", "))
			}
			args = append(args, st)
		}
		where = append(where, "t.status IN (?"+strings.Repeat(", ?", len(q.Statuses)-1)+")")
	} else if !q.IncludeDone {
		where = append(where, "t.status != 'done'")
	}
	if q.Product != "" {
		where = append(where, "pr.name = ? COLLATE NOCASE")
		args = append(args, q.Product)
	}
	if q.Parent != "" {
		parentID, err := s.resolveTask(ctx, q.Parent)
		if err != nil {
			return nil, 0, err
		}
		where = append(where, "t.parent_task_id = ?")
		args = append(args, parentID)
	}
	if q.RootsOnly {
		where = append(where, "t.parent_task_id IS NULL")
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	const from = ` FROM tasks t
		LEFT JOIN products pr ON pr.id = t.product_id
		LEFT JOIN tasks p ON p.id = t.parent_task_id
		LEFT JOIN tasks g ON g.id = p.parent_task_id `

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*)"+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.key, t.title, t.status, pr.name, p.key, g.key,
		  t.tracker_kind, t.tracker_id, t.tracker_url,
		  (SELECT count(*) FROM sessions s WHERE s.task_id = t.id),
		  (SELECT count(*) FROM observations o WHERE o.task_id = t.id AND o.deleted_at IS NULL),
		  (SELECT max(o.created_at) FROM observations o WHERE o.task_id = t.id AND o.deleted_at IS NULL),
		  (SELECT count(*) FROM tasks c WHERE c.parent_task_id = t.id),
		  (SELECT group_concat(w.path, char(10)) FROM worktrees w WHERE w.task_id = t.id)`+
		from+cond+` ORDER BY t.key COLLATE BINARY LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []TaskSummary{}
	for rows.Next() {
		var t TaskSummary
		var product, parent, grandparent, kind, trackerID, trackerURL, lastObs, paths sql.NullString
		if err := rows.Scan(&t.TaskID, &t.TaskKey, &t.Title, &t.Status, &product, &parent, &grandparent,
			&kind, &trackerID, &trackerURL, &t.SessionsCount, &t.ObservationsCount, &lastObs,
			&t.ChildrenCount, &paths); err != nil {
			return nil, 0, err
		}
		t.Product, t.ParentKey, t.LastObservationAt = ptr(product), ptr(parent), ptr(lastObs)
		t.AncestorKeys = ancestors(grandparent, parent)
		t.Tracker = tracker(kind, trackerID, trackerURL)
		for _, p := range splitLines(paths) {
			if pathAlive(p) {
				t.WorktreesAlive++
			}
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

type TaskCard struct {
	TaskID       string   `json:"task_id"`
	TaskKey      string   `json:"task_key"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Product      *string  `json:"product"`
	ParentKey    *string  `json:"parent_key"`
	AncestorKeys []string `json:"ancestor_keys"`
	Tracker      *Tracker `json:"tracker"`
	Notes        *string  `json:"notes"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type TaskNode struct {
	TaskKey           string     `json:"task_key"`
	Title             string     `json:"title"`
	Status            string     `json:"status"`
	ObservationsCount int        `json:"observations_count"`
	Children          []TaskNode `json:"children"`
}

type TaskSession struct {
	SessionID string  `json:"session_id"`
	Project   string  `json:"project"`
	Directory string  `json:"directory"`
	StartedAt string  `json:"started_at"`
	EndedAt   *string `json:"ended_at"`
	PinnedBy  *string `json:"pinned_by"`
}

type Worktree struct {
	ID               string  `json:"id"`
	Path             string  `json:"path"`
	Repo             string  `json:"repo"`
	Branch           string  `json:"branch"`
	Base             *string `json:"base"`
	Purpose          *string `json:"purpose"`
	TaskKey          *string `json:"task_key"`
	CreatedBySession *string `json:"created_by_session"`
	Alive            bool    `json:"alive"`
	CreatedAt        string  `json:"created_at"`
}

type HandoffRef struct {
	SyncID    string `json:"sync_id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

type ProgressRef struct {
	ID           string `json:"id"`
	DelegationID string `json:"delegation_id"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	Title        string `json:"title"`
	SessionID    string `json:"session_id"`
	CreatedAt    string `json:"created_at"`
}

type ObservationPreview struct {
	SyncID    string `json:"sync_id"`
	TaskKey   string `json:"task_key"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Project   string `json:"project"`
	CreatedAt string `json:"created_at"`
	Preview   string `json:"preview"`
}

// TaskDetail is the body of GET /tasks/{key}, task_show in docs/specs/api.md.
type TaskDetail struct {
	Task            TaskCard             `json:"task"`
	Children        *[]TaskNode          `json:"children,omitempty"`
	Sessions        []TaskSession        `json:"sessions"`
	Worktrees       []Worktree           `json:"worktrees"`
	Handoffs        []HandoffRef         `json:"handoffs"`
	ProgressReports []ProgressRef        `json:"progress_reports"`
	Observations    []ObservationPreview `json:"observations"`
}

type TaskShowQuery struct {
	IncludeSubtasks   bool
	ObservationsLimit int
}

// GetTask builds the task card. Handoffs have their own list, so the
// observation previews leave them out.
func (s *Store) GetTask(ctx context.Context, ref string, q TaskShowQuery) (TaskDetail, error) {
	if q.ObservationsLimit < 0 || q.ObservationsLimit > MaxObservationsLimit {
		return TaskDetail{}, errorf(ErrInvalidRequest, "observations_limit must be between 0 and %d", MaxObservationsLimit)
	}
	id, err := s.resolveTask(ctx, ref)
	if err != nil {
		return TaskDetail{}, err
	}
	var d TaskDetail
	if d.Task, err = taskCard(ctx, s.db, id); err != nil {
		return TaskDetail{}, err
	}

	if q.IncludeSubtasks {
		children, err := s.subtree(ctx, id)
		if err != nil {
			return TaskDetail{}, err
		}
		d.Children = &children
	}
	if d.Sessions, err = s.taskSessions(ctx, id); err != nil {
		return TaskDetail{}, err
	}
	if d.Worktrees, err = listWorktrees(ctx, s.db, "w.task_id = ?", []any{id}); err != nil {
		return TaskDetail{}, err
	}
	if d.Handoffs, err = s.taskHandoffs(ctx, id); err != nil {
		return TaskDetail{}, err
	}
	if d.ProgressReports, err = s.taskProgress(ctx, id); err != nil {
		return TaskDetail{}, err
	}
	if d.Observations, err = s.taskObservations(ctx, id, q); err != nil {
		return TaskDetail{}, err
	}
	return d, nil
}

// subtree returns the direct children of id, each with its own children; the
// three-level cap means two levels below id exhaust it.
func (s *Store) subtree(ctx context.Context, id string) ([]TaskNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.parent_task_id, t.key, t.title, t.status,
		  (SELECT count(*) FROM observations o WHERE o.task_id = t.id AND o.deleted_at IS NULL)
		FROM tasks t
		WHERE t.parent_task_id = ?1
		   OR t.parent_task_id IN (SELECT id FROM tasks WHERE parent_task_id = ?1)
		ORDER BY t.key COLLATE BINARY`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id, parent string
		node       TaskNode
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.parent, &r.node.TaskKey, &r.node.Title, &r.node.Status, &r.node.ObservationsCount); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []TaskNode{}
	for _, r := range all {
		if r.parent != id {
			continue
		}
		n := r.node
		n.Children = []TaskNode{}
		for _, g := range all {
			if g.parent == r.id {
				g.node.Children = []TaskNode{}
				n.Children = append(n.Children, g.node)
			}
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *Store) taskSessions(ctx context.Context, id string) ([]TaskSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project, directory, started_at, ended_at, pinned_by
		FROM sessions WHERE task_id = ? ORDER BY started_at DESC, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskSession{}
	for rows.Next() {
		var ss TaskSession
		var ended, by sql.NullString
		if err := rows.Scan(&ss.SessionID, &ss.Project, &ss.Directory, &ss.StartedAt, &ended, &by); err != nil {
			return nil, err
		}
		ss.EndedAt, ss.PinnedBy = ptr(ended), ptr(by)
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Store) taskHandoffs(ctx context.Context, id string) ([]HandoffRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sync_id, session_id, title, created_at FROM observations
		WHERE task_id = ? AND kind = 'handoff' AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffRef{}
	for rows.Next() {
		var h HandoffRef
		if err := rows.Scan(&h.SyncID, &h.SessionID, &h.Title, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) taskProgress(ctx context.Context, id string) ([]ProgressRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, delegation_id, role, status, title, session_id, created_at
		FROM progress_reports WHERE task_id = ? ORDER BY created_at DESC, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgressRef{}
	for rows.Next() {
		var p ProgressRef
		if err := rows.Scan(&p.ID, &p.DelegationID, &p.Role, &p.Status, &p.Title, &p.SessionID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) taskObservations(ctx context.Context, id string, q TaskShowQuery) ([]ObservationPreview, error) {
	out := []ObservationPreview{}
	if q.ObservationsLimit == 0 {
		return out, nil
	}
	scope := "o.task_id = ?1"
	if q.IncludeSubtasks {
		scope = `o.task_id IN (SELECT id FROM tasks WHERE id = ?1 OR parent_task_id = ?1
		   OR parent_task_id IN (SELECT id FROM tasks WHERE parent_task_id = ?1))`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.sync_id, t.key, o.title, o.type, o.project, o.created_at,
		  substr(o.content, 1, ?2)
		FROM observations o JOIN tasks t ON t.id = o.task_id
		WHERE `+scope+` AND o.kind IS NULL AND o.deleted_at IS NULL
		ORDER BY o.created_at DESC, o.id DESC LIMIT ?3`, id, observationPreviewLen, q.ObservationsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o ObservationPreview
		if err := rows.Scan(&o.SyncID, &o.TaskKey, &o.Title, &o.Type, &o.Project, &o.CreatedAt, &o.Preview); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type WorktreeQuery struct {
	Task      string
	Repo      string
	AliveOnly bool
	Limit     int
	Offset    int
}

// ListWorktrees computes liveness against the filesystem at call time, so
// AliveOnly filters and pages in memory.
func (s *Store) ListWorktrees(ctx context.Context, q WorktreeQuery) ([]Worktree, int, error) {
	if err := checkPage(&q.Limit, q.Offset, DefaultLimit); err != nil {
		return nil, 0, err
	}
	var where []string
	var args []any
	if q.Task != "" {
		id, err := s.resolveTask(ctx, q.Task)
		if err != nil {
			return nil, 0, err
		}
		where = append(where, "w.task_id = ?")
		args = append(args, id)
	}
	if q.Repo != "" {
		where = append(where, "w.repo = ?")
		args = append(args, q.Repo)
	}
	cond := "1"
	if len(where) > 0 {
		cond = strings.Join(where, " AND ")
	}
	all, err := listWorktrees(ctx, s.db, cond, args)
	if err != nil {
		return nil, 0, err
	}
	if q.AliveOnly {
		all = slices.DeleteFunc(all, func(w Worktree) bool { return !w.Alive })
	}
	total := len(all)
	lo := min(q.Offset, total)
	hi := min(lo+q.Limit, total)
	return all[lo:hi], total, nil
}

func listWorktrees(ctx context.Context, q querier, cond string, args []any) ([]Worktree, error) {
	rows, err := q.QueryContext(ctx, `SELECT w.id, w.path, w.repo, w.branch, w.base, w.purpose, t.key,
		  w.created_by_session, w.created_at
		FROM worktrees w LEFT JOIN tasks t ON t.id = w.task_id
		WHERE `+cond+` ORDER BY w.created_at DESC, w.path`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Worktree{}
	for rows.Next() {
		var w Worktree
		var base, purpose, key, by sql.NullString
		if err := rows.Scan(&w.ID, &w.Path, &w.Repo, &w.Branch, &base, &purpose, &key, &by, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.Base, w.Purpose, w.TaskKey, w.CreatedBySession = ptr(base), ptr(purpose), ptr(key), ptr(by)
		w.Alive = pathAlive(w.Path)
		out = append(out, w)
	}
	return out, rows.Err()
}

type Product struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	TasksCount     int     `json:"tasks_count"`
	OpenTasksCount int     `json:"open_tasks_count"`
}

type ProductQuery struct {
	Q      string
	Limit  int
	Offset int
}

// ListProducts filters by a case-insensitive substring of the name.
func (s *Store) ListProducts(ctx context.Context, q ProductQuery) ([]Product, int, error) {
	if err := checkPage(&q.Limit, q.Offset, DefaultLimit); err != nil {
		return nil, 0, err
	}
	cond, args := "", []any{}
	if q.Q != "" {
		cond = "WHERE instr(lower(p.name), lower(?)) > 0"
		args = append(args, q.Q)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM products p "+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, p.description,
		  (SELECT count(*) FROM tasks t WHERE t.product_id = p.id),
		  (SELECT count(*) FROM tasks t WHERE t.product_id = p.id AND t.status != 'done')
		FROM products p `+cond+` ORDER BY p.name COLLATE NOCASE LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		var p Product
		var desc sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &desc, &p.TasksCount, &p.OpenTasksCount); err != nil {
			return nil, 0, err
		}
		p.Description = ptr(desc)
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// resolveTask accepts the key, case-insensitive, or the ULID of tasks.id.
func (s *Store) resolveTask(ctx context.Context, ref string) (string, error) {
	return lookupTask(ctx, s.db, ref)
}

func lookupTask(ctx context.Context, q querier, ref string) (string, error) {
	if strings.EqualFold(ref, NoTaskKey) {
		return "", errorf(ErrUnknownTask, "%q is the explicit no-task pin, not a task", ref)
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM tasks WHERE key = ? OR id = ?`, ref, ref).Scan(&id)
	if err == sql.ErrNoRows {
		return "", errorf(ErrUnknownTask, "task %q does not exist", ref)
	}
	return id, err
}

func checkPage(limit *int, offset, def int) error {
	if *limit == 0 {
		*limit = def
	}
	if *limit < 1 || *limit > MaxLimit {
		return errorf(ErrInvalidRequest, "limit must be between 1 and %d", MaxLimit)
	}
	if offset < 0 {
		return errorf(ErrInvalidRequest, "offset must not be negative")
	}
	return nil
}

func pathAlive(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ptr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func ancestors(grandparent, parent sql.NullString) []string {
	out := []string{}
	for _, k := range []sql.NullString{grandparent, parent} {
		if k.Valid {
			out = append(out, k.String)
		}
	}
	return out
}

func tracker(kind, id, url sql.NullString) *Tracker {
	if !kind.Valid && !id.Valid && !url.Valid {
		return nil
	}
	return &Tracker{Kind: kind.String, ID: id.String, URL: url.String}
}

func splitLines(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	return strings.Split(ns.String, "\n")
}

func taskCard(ctx context.Context, q querier, id string) (TaskCard, error) {
	var c TaskCard
	var product, parent, grandparent, kind, trackerID, trackerURL, notes sql.NullString
	err := q.QueryRowContext(ctx, `SELECT t.id, t.key, t.title, t.status, pr.name, p.key, g.key,
		  t.tracker_kind, t.tracker_id, t.tracker_url, t.notes, t.created_at, t.updated_at
		FROM tasks t
		LEFT JOIN products pr ON pr.id = t.product_id
		LEFT JOIN tasks p ON p.id = t.parent_task_id
		LEFT JOIN tasks g ON g.id = p.parent_task_id
		WHERE t.id = ?`, id).Scan(&c.TaskID, &c.TaskKey, &c.Title, &c.Status, &product, &parent, &grandparent,
		&kind, &trackerID, &trackerURL, &notes, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return TaskCard{}, err
	}
	c.Product, c.ParentKey, c.Notes = ptr(product), ptr(parent), ptr(notes)
	c.AncestorKeys = ancestors(grandparent, parent)
	c.Tracker = tracker(kind, trackerID, trackerURL)
	return c, nil
}
