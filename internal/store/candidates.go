package store

import (
	"context"
	"database/sql"
)

// Threshold and cap of FindCandidates, from docs/specs/data-model.md (Candidatos de conflicto).
const (
	CandidateMinSimilarity = 0.8
	MaxCandidates          = 3
	previewLen             = 300
)

// Types and kind excluded on both sides: docs/specs/data-model.md (Candidatos de conflicto).
const (
	typeSessionSummary = "session_summary"
	typeHandoff        = "handoff"

	candidateTypeFilter = "o.type NOT IN ('" + typeSessionSummary + "', '" + typeHandoff + "') AND o.kind IS NOT ?"
)

func excludedFromCandidates(typ string, kind sql.NullString) bool {
	return typ == typeSessionSummary || typ == typeHandoff || kind.String == KindHandoff
}

type Candidate struct {
	JudgmentID string
	SyncID     string
	Title      string
	Type       string
	Project    string
	CreatedAt  string
	Similarity float64
	Preview    string
}

// FindCandidates runs the conflict search for an existing observation in its
// own transaction; AddObservation runs it inside the write transaction.
func (s *Store) FindCandidates(ctx context.Context, syncID string) ([]Candidate, error) {
	var out []Candidate
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM observations WHERE sync_id = ? AND deleted_at IS NULL`, syncID).Scan(&id)
		if err == sql.ErrNoRows {
			return errorf(ErrNotFound, "observation %q does not exist", syncID)
		}
		if err != nil {
			return err
		}
		out, err = findCandidates(ctx, tx, id)
		return err
	})
	return out, err
}

func findCandidates(ctx context.Context, tx *sql.Tx, id int64) ([]Candidate, error) {
	var title, project, scope, typ string
	var kind sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT title, project, scope, type, kind FROM observations WHERE id = ?`, id).
		Scan(&title, &project, &scope, &typ, &kind)
	if err != nil {
		return nil, err
	}
	if excludedFromCandidates(typ, kind) {
		return nil, nil
	}
	match, err := observationMatch(title, MatchAny)
	if err != nil {
		return nil, nil
	}
	var selfRank float64
	err = tx.QueryRowContext(ctx, `SELECT rank FROM observations_fts WHERE observations_fts MATCH ? AND rowid = ?`,
		match, id).Scan(&selfRank)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if selfRank == 0 {
		return nil, nil
	}
	args := []any{match, project, scope, id}
	args = append(args, KindHandoff, MaxCandidates)
	rows, err := tx.QueryContext(ctx, `SELECT o.sync_id, o.title, o.type, o.project, o.created_at, o.content, f.rank
		FROM observations_fts f JOIN observations o ON o.id = f.rowid
		WHERE observations_fts MATCH ? AND o.project = ? AND o.scope = ?
		  AND o.deleted_at IS NULL AND o.id != ? AND `+candidateTypeFilter+`
		ORDER BY f.rank LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var content string
		var rank float64
		if err := rows.Scan(&c.SyncID, &c.Title, &c.Type, &c.Project, &c.CreatedAt, &content, &rank); err != nil {
			return nil, err
		}
		c.Similarity = rank / selfRank
		if c.Similarity < CandidateMinSimilarity {
			break
		}
		c.Preview = preview(content)
		out = append(out, c)
	}
	return out, rows.Err()
}

func preview(s string) string {
	r := []rune(s)
	if len(r) <= previewLen {
		return s
	}
	return string(r[:previewLen])
}
