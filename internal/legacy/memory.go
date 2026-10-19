package legacy

import (
	"context"
	"regexp"
)

// controlProject holds the flow records; they become rows of the flow tables,
// not observations (docs/specs/import-legacy.md, Fase 2).
const controlProject = "tasks"

var importSessions = isoColumns(`
INSERT INTO sessions (id, project, directory, started_at, ended_at, summary, created_at, updated_at)
SELECT id, project, directory, {started_at}, {ended_at}, summary, {started_at}, {coalesce(ended_at, started_at)}
  FROM legacy.sessions WHERE true
ON CONFLICT(id) DO UPDATE SET
  project = excluded.project, directory = excluded.directory, started_at = excluded.started_at,
  ended_at = excluded.ended_at, summary = excluded.summary, updated_at = excluded.updated_at`)

var importObservations = isoColumns(`
INSERT INTO observations (id, sync_id, session_id, type, title, content, project, scope, topic_key, tool_name,
  normalized_hash, revision_count, duplicate_count, last_seen_at, review_after, pinned, created_at, updated_at, deleted_at)
SELECT id, sync_id, session_id, type, title, content, project, scope, topic_key, tool_name,
  normalized_hash, revision_count, duplicate_count, {last_seen_at}, {review_after}, coalesce(pinned, 0) != 0,
  {created_at}, {updated_at}, {deleted_at}
  FROM legacy.observations WHERE project != ?
ON CONFLICT(sync_id) DO UPDATE SET
  session_id = excluded.session_id, type = excluded.type, title = excluded.title, content = excluded.content,
  project = excluded.project, scope = excluded.scope, topic_key = excluded.topic_key, tool_name = excluded.tool_name,
  normalized_hash = excluded.normalized_hash, revision_count = excluded.revision_count,
  duplicate_count = excluded.duplicate_count, last_seen_at = excluded.last_seen_at,
  review_after = excluded.review_after, pinned = excluded.pinned, created_at = excluded.created_at,
  updated_at = excluded.updated_at, deleted_at = excluded.deleted_at`)

var importPrompts = isoColumns(`
INSERT INTO user_prompts (id, sync_id, session_id, content, project, created_at)
SELECT id, sync_id, session_id, content, project, {created_at} FROM legacy.user_prompts WHERE true
ON CONFLICT(sync_id) DO UPDATE SET
  session_id = excluded.session_id, content = excluded.content, project = excluded.project,
  created_at = excluded.created_at`)

// importableRelation keeps relations whose endpoints were imported as
// observations; the rest point at control records that became flow rows.
const importableRelation = `
(r.source_id IS NULL OR r.source_id IN (SELECT sync_id FROM main.observations))
AND (r.target_id IS NULL OR r.target_id IN (SELECT sync_id FROM main.observations))`

var importRelations = isoColumns(`
INSERT INTO memory_relations (id, sync_id, source_id, target_id, relation, reason, evidence, confidence,
  judgment_status, marked_by_actor, marked_by_kind, marked_by_model, session_id, superseded_at,
  superseded_by_relation_id, created_at, updated_at)
SELECT id, sync_id, source_id, target_id, relation, reason, evidence, confidence,
  judgment_status, marked_by_actor, marked_by_kind, marked_by_model, session_id, {superseded_at},
  superseded_by_relation_id, {created_at}, {updated_at}
  FROM legacy.memory_relations r WHERE ` + importableRelation + `
ON CONFLICT(sync_id) DO UPDATE SET
  source_id = excluded.source_id, target_id = excluded.target_id, relation = excluded.relation,
  reason = excluded.reason, evidence = excluded.evidence, confidence = excluded.confidence,
  judgment_status = excluded.judgment_status, marked_by_actor = excluded.marked_by_actor,
  marked_by_kind = excluded.marked_by_kind, marked_by_model = excluded.marked_by_model,
  session_id = excluded.session_id, superseded_at = excluded.superseded_at,
  superseded_by_relation_id = excluded.superseded_by_relation_id, updated_at = excluded.updated_at`)

func (im *importer) importMemory(ctx context.Context) error {
	if _, err := im.exec(ctx, importSessions); err != nil {
		return err
	}
	if _, err := im.exec(ctx, importObservations, controlProject); err != nil {
		return err
	}
	if _, err := im.exec(ctx, importPrompts); err != nil {
		return err
	}
	return im.tx.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM legacy.sessions),
		       (SELECT count(*) FROM legacy.observations WHERE project != ?),
		       (SELECT count(*) FROM legacy.observations WHERE project = ?),
		       (SELECT count(*) FROM legacy.user_prompts)`, controlProject, controlProject).
		Scan(&im.sum.Sessions, &im.sum.Observations, &im.sum.ControlRecords, &im.sum.Prompts)
}

func (im *importer) importRelations(ctx context.Context) error {
	if _, err := im.exec(ctx, importRelations); err != nil {
		return err
	}
	if _, err := im.exec(ctx, `
		UPDATE memory_relations SET superseded_by_relation_id = NULL
		 WHERE superseded_by_relation_id NOT IN (SELECT id FROM memory_relations)`); err != nil {
		return err
	}
	err := im.tx.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(judgment_status = 'judged'), 0), coalesce(sum(judgment_status = 'pending'), 0)
		  FROM legacy.memory_relations r WHERE `+importableRelation).
		Scan(&im.sum.Relations, &im.sum.RelationsJudged, &im.sum.RelationsPending)
	if err != nil {
		return err
	}
	rows, err := im.tx.QueryContext(ctx,
		"SELECT sync_id FROM legacy.memory_relations r WHERE NOT ("+importableRelation+") ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		im.sum.RelationsSkipped = append(im.sum.RelationsSkipped, id)
	}
	return rows.Err()
}

// isoLayout is the timestamp shape the store writes and compares as text.
const isoLayout = "%Y-%m-%dT%H:%M:%SZ"

var isoPlaceholder = regexp.MustCompile(`\{([^{}]+)\}`)

// isoColumns rewrites each {expr} into expr normalized to isoLayout; the
// timestamps check guarantees beforehand that every value parses.
func isoColumns(query string) string {
	return isoPlaceholder.ReplaceAllString(query, "strftime('"+isoLayout+"', $1)")
}
