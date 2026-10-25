package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const pinnedByImport = "import"

type controlRow struct {
	id                            int64
	syncID, sessionID, typ, title string
	content                       string
	scope, topicKey               string
	hash, lastSeen, reviewAfter   sql.NullString
	revisionCount, duplicateCount int
	pinned                        int
	createdAt, updatedAt          string
	deletedAt                     sql.NullString
	ref                           controlRef
}

type taskRow struct {
	key, title, status     string
	hasIdentity            bool
	product                string
	trackerKind, trackerID string
	trackerURL, notes      string
	parentHint, parentRule string
	parent                 *taskRow
	parentFromHint         bool
	createdAt, updatedAt   string
	dbID                   string
}

type pinRef struct {
	task *taskRow
	row  *controlRow
}

type worktreeRef struct {
	wt   worktree
	task *taskRow
	row  *controlRow
}

type flowModel struct {
	byRef map[string]*taskRow
	tasks []*taskRow
}

func (m *flowModel) touch(t *taskRow, createdAt, updatedAt string) {
	if t.createdAt == "" || createdAt < t.createdAt {
		t.createdAt = createdAt
	}
	if updatedAt > t.updatedAt {
		t.updatedAt = updatedAt
	}
}

// resolve finds the task a topic_key segment or footer names, creating it
// with title = key and status = todo when no identity defines it.
func (m *flowModel) resolve(ref, footerKey, createdAt, updatedAt string) *taskRow {
	if noTaskKeys[strings.ToLower(ref)] {
		return nil
	}
	t, ok := m.byRef[strings.ToLower(ref)]
	if !ok {
		key := ref
		if strings.EqualFold(footerKey, ref) {
			key = footerKey
		}
		t = &taskRow{key: key, title: key, status: "todo"}
		m.byRef[strings.ToLower(ref)] = t
		m.tasks = append(m.tasks, t)
	}
	m.touch(t, createdAt, updatedAt)
	return t
}

func (im *importer) importFlow(ctx context.Context) error {
	rows, err := im.loadControlRows(ctx)
	if err != nil {
		return err
	}
	m := &flowModel{byRef: map[string]*taskRow{}}
	var live, plain []*controlRow
	for _, r := range rows {
		switch {
		case r.ref.kind == "":
			plain = append(plain, r)
		case r.deletedAt.Valid && r.ref.kind != kindHandoff:
			im.sum.DeletedControl = append(im.sum.DeletedControl, r.topicKey)
		default:
			live = append(live, r)
		}
	}
	for _, r := range live {
		if r.ref.kind == kindIdentity {
			im.applyIdentity(m, r)
		}
	}

	pins := map[string]pinRef{}
	var pinOrder []string
	worktrees := map[string]worktreeRef{}
	var wtOrder []string
	var handoffs, reports []pinRef
	for _, r := range live {
		f, _ := lastFooter(r.content)
		var t *taskRow
		if r.ref.kind != kindIdentity {
			t = m.resolve(r.ref.taskRef, f.key, r.createdAt, r.updatedAt)
		}
		switch r.ref.kind {
		case kindStatus:
			s, ok := parseStatus(r.content)
			if !ok {
				im.sum.StatusWarnings = append(im.sum.StatusWarnings, r.topicKey)
			}
			if t != nil {
				t.status = s
			}
		case kindSession:
			im.sum.Pins++
			if _, seen := pins[r.ref.session]; seen {
				im.sum.PinsSuperseded++
			} else {
				pinOrder = append(pinOrder, r.ref.session)
			}
			pins[r.ref.session] = pinRef{task: t, row: r}
		case kindWorktree:
			wt, ok := parseWorktree(r.content)
			if !ok {
				im.sum.WorktreesUnparseable = append(im.sum.WorktreesUnparseable, r.topicKey)
				continue
			}
			if _, seen := worktrees[wt.Path]; seen {
				im.sum.WorktreesOverwritten++
			} else {
				wtOrder = append(wtOrder, wt.Path)
			}
			worktrees[wt.Path] = worktreeRef{wt: wt, task: t, row: r}
		case kindHandoff:
			handoffs = append(handoffs, pinRef{task: t, row: r})
		case kindProgress:
			reports = append(reports, pinRef{task: t, row: r})
		}
	}

	footers, err := im.collectFooters(ctx, m)
	if err != nil {
		return err
	}
	var plainFooters []footerAssignment
	for _, r := range plain {
		if t := im.footerTask(m, r.syncID, r.content, r.createdAt, r.updatedAt); t != nil {
			plainFooters = append(plainFooters, footerAssignment{syncID: r.syncID, task: t})
		}
	}
	if err := im.writeTasks(ctx, m); err != nil {
		return err
	}
	if err := im.writeParents(ctx, m); err != nil {
		return err
	}
	if err := im.writePins(ctx, pins, pinOrder); err != nil {
		return err
	}
	if err := im.writeWorktrees(ctx, worktrees, wtOrder); err != nil {
		return err
	}
	if err := im.writeHandoffs(ctx, handoffs); err != nil {
		return err
	}
	if err := im.writeProgress(ctx, reports); err != nil {
		return err
	}
	if err := im.writePlain(ctx, plain); err != nil {
		return err
	}
	footers = append(footers, plainFooters...)
	return im.writeFooters(ctx, footers)
}

func (im *importer) loadControlRows(ctx context.Context) ([]*controlRow, error) {
	rows, err := im.tx.QueryContext(ctx, isoColumns(`
		SELECT id, sync_id, session_id, type, title, content, scope, coalesce(topic_key, ''), normalized_hash,
		       revision_count, duplicate_count, {last_seen_at}, {review_after}, coalesce(pinned, 0) != 0,
		       {created_at}, {updated_at}, {deleted_at}
		  FROM legacy.observations WHERE project = ? ORDER BY id`), controlProject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*controlRow
	for rows.Next() {
		r := &controlRow{}
		if err := rows.Scan(&r.id, &r.syncID, &r.sessionID, &r.typ, &r.title, &r.content, &r.scope, &r.topicKey, &r.hash,
			&r.revisionCount, &r.duplicateCount, &r.lastSeen, &r.reviewAfter, &r.pinned,
			&r.createdAt, &r.updatedAt, &r.deletedAt); err != nil {
			return nil, err
		}
		r.ref, _ = parseControlKey(r.topicKey)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (im *importer) applyIdentity(m *flowModel, r *controlRow) {
	id := parseIdentity(r.content)
	key := id.key
	if key == "" {
		key = r.ref.taskRef
	}
	t, ok := m.byRef[strings.ToLower(key)]
	if !ok {
		t = &taskRow{key: key, status: "todo"}
		m.tasks = append(m.tasks, t)
	}
	m.byRef[strings.ToLower(key)] = t
	m.byRef[strings.ToLower(r.ref.taskRef)] = t
	t.hasIdentity = true
	t.title = id.title
	if t.title == "" {
		t.title = key
	}
	t.product, t.notes = id.product, id.notes
	t.trackerKind, t.trackerID, t.trackerURL = id.trackerKind, id.trackerID, id.trackerURL
	t.parentHint, t.parentRule = id.parentHintedKey, id.parentRuleKey
	m.touch(t, r.createdAt, r.updatedAt)
}

type footerAssignment struct {
	syncID string
	task   *taskRow
}

func (im *importer) collectFooters(ctx context.Context, m *flowModel) ([]footerAssignment, error) {
	rows, err := im.tx.QueryContext(ctx,
		isoColumns("SELECT sync_id, content, {created_at}, {updated_at} FROM legacy.observations WHERE project != ? ORDER BY id"),
		controlProject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []footerAssignment
	for rows.Next() {
		var syncID, content, createdAt, updatedAt string
		if err := rows.Scan(&syncID, &content, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if t := im.footerTask(m, syncID, content, createdAt, updatedAt); t != nil {
			out = append(out, footerAssignment{syncID: syncID, task: t})
		}
	}
	return out, rows.Err()
}

// footerTask resolves the `**Task**` footer of an imported observation and
// counts it; nil means no footer, "ninguna", or an unresolvable value.
func (im *importer) footerTask(m *flowModel, syncID, content, createdAt, updatedAt string) *taskRow {
	f, ok := lastFooter(content)
	if !ok {
		return nil
	}
	im.sum.Footers++
	switch {
	case f.none:
		im.sum.FootersNone++
	case f.key == "":
		im.sum.FootersUnresolved = append(im.sum.FootersUnresolved, syncID)
	default:
		im.sum.FootersWithTask++
		return m.resolve(f.key, f.key, createdAt, updatedAt)
	}
	return nil
}

func (im *importer) writeTasks(ctx context.Context, m *flowModel) error {
	productIDs := map[string]string{}
	for _, t := range m.tasks {
		var productID sql.NullString
		if t.product != "" {
			name := canonicalProduct(t.product)
			if _, seen := im.sum.ProductMap[t.product]; !seen {
				im.sum.addProduct(t.product, name)
			}
			if name != "" {
				id, ok := productIDs[strings.ToLower(name)]
				if !ok {
					var err error
					if id, err = im.upsertProduct(ctx, name); err != nil {
						return err
					}
					productIDs[strings.ToLower(name)] = id
				}
				productID = sql.NullString{String: id, Valid: true}
			}
		}
		_, err := im.exec(ctx, `
			INSERT INTO tasks (id, key, title, product_id, status, tracker_kind, tracker_id, tracker_url, notes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
			  title = excluded.title, product_id = excluded.product_id, status = excluded.status,
			  tracker_kind = excluded.tracker_kind, tracker_id = excluded.tracker_id,
			  tracker_url = excluded.tracker_url, notes = excluded.notes, updated_at = excluded.updated_at`,
			newID(im.opts.Now()), t.key, t.title, productID, t.status,
			nullIfEmpty(t.trackerKind), nullIfEmpty(t.trackerID), nullIfEmpty(t.trackerURL), nullIfEmpty(t.notes),
			t.createdAt, t.updatedAt)
		if err != nil {
			return fmt.Errorf("task %s: %w", t.key, err)
		}
		if err := im.tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE key = ?", t.key).Scan(&t.dbID); err != nil {
			return err
		}
		im.sum.Tasks++
		if t.hasIdentity {
			im.sum.TasksWithIdentity++
		} else {
			im.sum.TasksWithoutIdentity = append(im.sum.TasksWithoutIdentity, t.key)
		}
	}
	im.sum.Products = len(productIDs)
	return nil
}

const maxTaskLevels = 3

// writeParents sets parent_task_id from the explicit **Product** hint, or
// failing that from productParentRules. A hint naming a task that is not in
// the store gives no parent; one that would exceed maxTaskLevels or close a
// cycle is dropped.
func (im *importer) writeParents(ctx context.Context, m *flowModel) error {
	for _, t := range m.tasks {
		t.parent, t.parentFromHint = nil, false
		if t.parentHint != "" && !strings.EqualFold(t.parentHint, t.key) {
			if p := m.byRef[strings.ToLower(t.parentHint)]; p != nil {
				t.parent, t.parentFromHint = p, true
			} else {
				im.sum.ParentHintsUnresolved = append(im.sum.ParentHintsUnresolved, t.key+" → "+t.parentHint)
			}
		}
		if t.parent == nil && t.parentRule != "" {
			if p := m.byRef[strings.ToLower(t.parentRule)]; p != nil && p != t {
				t.parent = p
			}
		}
	}
	for dropped := true; dropped; {
		dropped = false
		for _, t := range m.tasks {
			if t.parent != nil && !withinMaxLevels(t) {
				im.sum.ParentsDropped = append(im.sum.ParentsDropped, t.key+" → "+t.parent.key)
				t.parent, dropped = nil, true
			}
		}
	}
	for _, t := range m.tasks {
		var parentID sql.NullString
		if t.parent != nil {
			parentID = sql.NullString{String: t.parent.dbID, Valid: true}
			if t.parentFromHint {
				im.sum.ParentsFromHint++
			} else {
				im.sum.ParentsFromRule++
			}
		}
		if _, err := im.exec(ctx, "UPDATE tasks SET parent_task_id = ? WHERE id = ?", parentID, t.dbID); err != nil {
			return fmt.Errorf("parent of %s: %w", t.key, err)
		}
	}
	return im.tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE parent_task_id IS NULL").Scan(&im.sum.Epics)
}

// withinMaxLevels is false when t's chain of parents loops or t sits deeper
// than maxTaskLevels.
func withinMaxLevels(t *taskRow) bool {
	level := 1
	for p := t.parent; p != nil; p = p.parent {
		level++
		if p == t || level > maxTaskLevels {
			return false
		}
	}
	return true
}

func (im *importer) upsertProduct(ctx context.Context, name string) (string, error) {
	if _, err := im.exec(ctx,
		"INSERT INTO products (id, name, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(name) DO NOTHING",
		newID(im.opts.Now()), name, im.now, im.now); err != nil {
		return "", fmt.Errorf("product %s: %w", name, err)
	}
	var id string
	err := im.tx.QueryRowContext(ctx, "SELECT id FROM products WHERE name = ?", name).Scan(&id)
	return id, err
}

func (im *importer) writePins(ctx context.Context, pins map[string]pinRef, order []string) error {
	for _, session := range order {
		p := pins[session]
		n, err := im.exec(ctx,
			"UPDATE sessions SET task_id = ?, pinned_at = created_at, pinned_by = ? WHERE id = ?",
			taskID(p.task), pinnedByImport, session)
		if err != nil {
			return fmt.Errorf("pin %s: %w", session, err)
		}
		switch {
		case n == 0:
			im.sum.PinsMissingSession = append(im.sum.PinsMissingSession, p.row.topicKey)
		case p.task == nil:
			im.sum.PinsNone++
		default:
			im.sum.PinsWithTask++
		}
	}
	return nil
}

func (im *importer) writeWorktrees(ctx context.Context, wts map[string]worktreeRef, order []string) error {
	for _, path := range order {
		w := wts[path]
		_, err := im.exec(ctx, `
			INSERT INTO worktrees (id, task_id, path, repo, branch, base, purpose, created_by_session, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
			  task_id = excluded.task_id, repo = excluded.repo, branch = excluded.branch, base = excluded.base,
			  purpose = excluded.purpose, created_by_session = excluded.created_by_session, updated_at = excluded.updated_at`,
			newID(im.opts.Now()), taskID(w.task), path, w.wt.Repo, w.wt.Branch, nullIfEmpty(w.wt.Base),
			nullIfEmpty(w.wt.Purpose), nullIfEmpty(w.wt.CreatedBySession), w.row.createdAt, w.row.updatedAt)
		if err != nil {
			return fmt.Errorf("worktree %s: %w", path, err)
		}
		im.sum.Worktrees++
		if im.opts.PathExists(path) {
			im.sum.WorktreesAlive++
		} else {
			im.sum.WorktreesDead++
		}
	}
	return nil
}

func (im *importer) writeHandoffs(ctx context.Context, handoffs []pinRef) error {
	for _, h := range handoffs {
		r := h.row
		var project string
		err := im.tx.QueryRowContext(ctx, "SELECT project FROM sessions WHERE id = ?", r.ref.session).Scan(&project)
		if err == sql.ErrNoRows {
			im.sum.HandoffsSkipped = append(im.sum.HandoffsSkipped, r.topicKey)
			continue
		}
		if err != nil {
			return err
		}
		if err := im.insertObservation(ctx, r, r.ref.session, project, h.task, "handoff"); err != nil {
			return fmt.Errorf("handoff %s: %w", r.topicKey, err)
		}
		im.sum.Handoffs++
	}
	return nil
}

// writePlain imports the records of the tasks project that are not control
// records as ordinary observations (docs/specs/import-legacy.md, Fase 2).
func (im *importer) writePlain(ctx context.Context, plain []*controlRow) error {
	for _, r := range plain {
		if err := im.insertObservation(ctx, r, r.sessionID, controlProject, nil, ""); err != nil {
			return fmt.Errorf("observation %s: %w", r.syncID, err)
		}
		label := r.topicKey
		if label == "" {
			label = fmt.Sprintf("#%d sin topic_key: %s", r.id, r.title)
		}
		im.sum.PlainImported = append(im.sum.PlainImported, label)
	}
	return nil
}

func (im *importer) insertObservation(ctx context.Context, r *controlRow, sessionID, project string, task *taskRow, kind string) error {
	_, err := im.exec(ctx, `
		INSERT INTO observations (id, sync_id, session_id, type, title, content, project, scope, topic_key,
		  task_id, kind, normalized_hash, revision_count, duplicate_count, last_seen_at, review_after, pinned,
		  created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sync_id) DO UPDATE SET
		  session_id = excluded.session_id, type = excluded.type, title = excluded.title,
		  content = excluded.content, project = excluded.project, scope = excluded.scope,
		  topic_key = excluded.topic_key, task_id = excluded.task_id, kind = excluded.kind,
		  normalized_hash = excluded.normalized_hash, revision_count = excluded.revision_count,
		  duplicate_count = excluded.duplicate_count, last_seen_at = excluded.last_seen_at,
		  review_after = excluded.review_after, pinned = excluded.pinned,
		  updated_at = excluded.updated_at, deleted_at = excluded.deleted_at`,
		r.id, r.syncID, sessionID, r.typ, r.title, r.content, project, r.scope, nullIfEmpty(r.topicKey),
		taskID(task), nullIfEmpty(kind), r.hash, r.revisionCount, r.duplicateCount, r.lastSeen, r.reviewAfter,
		r.pinned, r.createdAt, r.updatedAt, r.deletedAt)
	return err
}

func (im *importer) writeProgress(ctx context.Context, reports []pinRef) error {
	written := map[string]bool{}
	for _, p := range reports {
		r := p.row
		role, report, ok := parseProgress(r.ref.slot, r.content)
		if !ok {
			im.sum.ProgressSkipped = append(im.sum.ProgressSkipped, r.topicKey+" (sin JSON válido)")
			continue
		}
		var exists int
		if err := im.tx.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE id = ?", r.ref.session).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			im.sum.ProgressSkipped = append(im.sum.ProgressSkipped, r.topicKey+" (sesión inexistente)")
			continue
		}
		summary, _ := json.Marshal(nonNil(report.Summary))
		next, _ := json.Marshal(nonNil(report.NextSteps))
		var refs sql.NullString
		if len(report.References) > 0 && string(report.References) != "null" {
			refs = sql.NullString{String: string(report.References), Valid: true}
		}
		_, err := im.exec(ctx, `
			INSERT INTO progress_reports (id, session_id, delegation_id, task_id, role, status, title, summary,
			  next_steps, refs, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, delegation_id) DO UPDATE SET
			  task_id = excluded.task_id, role = excluded.role, status = excluded.status, title = excluded.title,
			  summary = excluded.summary, next_steps = excluded.next_steps, refs = excluded.refs,
			  updated_at = excluded.updated_at`,
			newID(im.opts.Now()), r.ref.session, r.ref.slot, taskID(p.task), role, report.Status, r.title,
			string(summary), string(next), refs, r.createdAt, r.updatedAt)
		if err != nil {
			return fmt.Errorf("progress %s: %w", r.topicKey, err)
		}
		if key := r.ref.session + "/" + r.ref.slot; written[key] {
			im.sum.ProgressSuperseded++
		} else {
			written[key] = true
			im.sum.Progress++
		}
	}
	return nil
}

func (im *importer) writeFooters(ctx context.Context, footers []footerAssignment) error {
	stmt, err := im.tx.PrepareContext(ctx, "UPDATE observations SET task_id = ? WHERE sync_id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, f := range footers {
		if _, err := stmt.ExecContext(ctx, f.task.dbID, f.syncID); err != nil {
			return fmt.Errorf("footer %s: %w", f.syncID, err)
		}
	}
	return nil
}

func taskID(t *taskRow) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.dbID, Valid: true}
}

func nullIfEmpty(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nonNil(s stringList) stringList {
	if s == nil {
		return []string{}
	}
	return s
}
