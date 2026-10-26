package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

const (
	RelationPending = "pending"

	StatusPending    = "pending"
	StatusJudged     = "judged"
	StatusSuperseded = "superseded"
)

// JudgeRelations is the relation enum of mem_judge in docs/specs/api.md.
var JudgeRelations = []string{"supersedes", "conflicts_with", "related", "compatible", "scoped", "not_conflict"}

type JudgmentInput struct {
	JudgmentID string
	Relation   string
	Reason     string
	Evidence   string
	Confidence *float64
	SessionID  string
}

// Judgment is the mem_judge response. SupersededRelationID is the judgment_id
// of the judgment this one replaced, or empty on a first judgment.
type Judgment struct {
	JudgmentID           string
	SourceSyncID         string
	TargetSyncID         string
	Relation             string
	JudgmentStatus       string
	Confidence           *float64
	SessionID            string
	SupersededRelationID string
	UpdatedAt            string
}

// createPendingRelations attaches a relation to each candidate. A pair is
// unordered: a judged relation in either direction is kept as is, otherwise a
// pending one in either direction is reused, otherwise a new pending is written.
func createPendingRelations(ctx context.Context, tx *sql.Tx, now, sessionID, sourceSyncID string, cands []Candidate) ([]Candidate, error) {
	for i := range cands {
		err := tx.QueryRowContext(ctx, `SELECT sync_id, relation, judgment_status FROM memory_relations
			WHERE ((source_id = ? AND target_id = ?) OR (source_id = ? AND target_id = ?))
			  AND judgment_status IN (?, ?)
			ORDER BY judgment_status = ? DESC, id DESC LIMIT 1`,
			sourceSyncID, cands[i].SyncID, cands[i].SyncID, sourceSyncID,
			StatusPending, StatusJudged, StatusJudged).
			Scan(&cands[i].JudgmentID, &cands[i].Relation, &cands[i].JudgmentStatus)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		cands[i].JudgmentID = newSyncID("rel-")
		cands[i].Relation, cands[i].JudgmentStatus = RelationPending, StatusPending
		_, err = tx.ExecContext(ctx, `INSERT INTO memory_relations
			  (sync_id, source_id, target_id, relation, judgment_status, session_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			cands[i].JudgmentID, sourceSyncID, cands[i].SyncID, RelationPending, StatusPending, sessionID, now, now)
		if err != nil {
			return nil, err
		}
	}
	return cands, nil
}

// chainHead follows superseded_by_relation_id to the live relation.
func chainHead(ctx context.Context, tx *sql.Tx, pk int64) (string, error) {
	var syncID string
	var next sql.NullInt64
	for {
		err := tx.QueryRowContext(ctx, `SELECT sync_id, superseded_by_relation_id FROM memory_relations WHERE id = ?`, pk).
			Scan(&syncID, &next)
		if err != nil || !next.Valid {
			return syncID, err
		}
		pk = next.Int64
	}
}

// JudgeRelation resolves a pending relation. Judging an already judged one
// inserts a new relation and marks the previous as superseded by it.
func (s *Store) JudgeRelation(ctx context.Context, in JudgmentInput) (Judgment, error) {
	if !slices.Contains(JudgeRelations, in.Relation) {
		return Judgment{}, errorf(ErrInvalidEnum, "relation must be one of %s", strings.Join(JudgeRelations, ", "))
	}
	if in.JudgmentID == "" || in.SessionID == "" {
		return Judgment{}, errorf(ErrInvalidRequest, "judgment_id and session_id are required")
	}
	if in.Confidence != nil && (*in.Confidence < 0 || *in.Confidence > 1) {
		return Judgment{}, errorf(ErrInvalidRequest, "confidence must be between 0 and 1")
	}
	var out Judgment
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := getSession(ctx, tx, in.SessionID); err != nil {
			return err
		}
		var pk int64
		var source, target, status string
		err := tx.QueryRowContext(ctx, `SELECT id, source_id, target_id, judgment_status
			FROM memory_relations WHERE sync_id = ?`, in.JudgmentID).Scan(&pk, &source, &target, &status)
		if err == sql.ErrNoRows {
			return errorf(ErrNotFound, "judgment %q does not exist", in.JudgmentID)
		}
		if err != nil {
			return err
		}
		if status == StatusSuperseded {
			head, err := chainHead(ctx, tx, pk)
			if err != nil {
				return err
			}
			return errorf(ErrInvalidRequest, "judgment %q was superseded; judge its current head %q", in.JudgmentID, head)
		}
		now := s.timestamp()
		out = Judgment{JudgmentID: in.JudgmentID, SourceSyncID: source, TargetSyncID: target,
			Relation: in.Relation, JudgmentStatus: StatusJudged, Confidence: in.Confidence,
			SessionID: in.SessionID, UpdatedAt: now}
		if status == StatusPending {
			_, err = tx.ExecContext(ctx, `UPDATE memory_relations SET relation = ?, reason = ?, evidence = ?,
				  confidence = ?, judgment_status = ?, session_id = ?, updated_at = ? WHERE id = ?`,
				in.Relation, nullable(in.Reason), nullable(in.Evidence), in.Confidence, StatusJudged, in.SessionID, now, pk)
			return err
		}
		out.JudgmentID = newSyncID("rel-")
		out.SupersededRelationID = in.JudgmentID
		r, err := tx.ExecContext(ctx, `INSERT INTO memory_relations
			  (sync_id, source_id, target_id, relation, reason, evidence, confidence, judgment_status,
			   session_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			out.JudgmentID, source, target, in.Relation, nullable(in.Reason), nullable(in.Evidence),
			in.Confidence, StatusJudged, in.SessionID, now, now)
		if err != nil {
			return err
		}
		newPK, err := r.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_relations SET judgment_status = ?, superseded_at = ?,
			  superseded_by_relation_id = ?, updated_at = ? WHERE id = ?`, StatusSuperseded, now, newPK, now, pk)
		return err
	})
	return out, err
}
