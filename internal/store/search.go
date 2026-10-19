package store

import (
	"context"
	"strings"
)

const (
	MatchAll = "all"
	MatchAny = "any"

	DefaultSearchLimit = 20
	MaxSearchLimit     = 200
)

// FTSQuery turns a user query into a literal FTS5 MATCH expression, as fixed
// in docs/specs/data-model.md (Búsqueda de texto). Callers scope it to
// columns; see observationMatch and promptMatch.
func FTSQuery(query, mode string) (string, error) {
	sep := " "
	switch mode {
	case "", MatchAll:
	case MatchAny:
		sep = " OR "
	default:
		return "", errorf(ErrInvalidRequest, "match_mode must be all or any")
	}
	var terms []string
	for _, f := range strings.Fields(query) {
		if t := strings.ReplaceAll(f, `"`, ""); t != "" {
			terms = append(terms, `"`+t+`"`)
		}
	}
	if len(terms) == 0 {
		return "", errorf(ErrInvalidRequest, "query has no searchable terms")
	}
	return strings.Join(terms, sep), nil
}

func observationMatch(query, mode string) (string, error) {
	return columnMatch("{title content}", query, mode)
}

func promptMatch(query string) (string, error) {
	return columnMatch("content", query, MatchAll)
}

func columnMatch(columns, query, mode string) (string, error) {
	expr, err := FTSQuery(query, mode)
	if err != nil {
		return "", err
	}
	return columns + ": (" + expr + ")", nil
}

// SearchParams filters SearchObservations. Empty fields do not filter; an
// empty Query lists by created_at DESC instead of ranking.
type SearchParams struct {
	Query     string
	MatchMode string
	Project   string
	Scope     string
	Kind      string
	SessionID string
	Types     []string
	TaskIDs   []string
	Since     string
	Until     string
	Limit     int
	Offset    int
}

type SearchResult struct {
	Observation
	Score float64
}

// SearchObservations returns one page of matches and the total count.
// Score is -rank, so higher is better.
func (s *Store) SearchObservations(ctx context.Context, p SearchParams) ([]SearchResult, int, error) {
	if p.Limit == 0 {
		p.Limit = DefaultSearchLimit
	}
	if p.Limit < 0 || p.Limit > MaxSearchLimit || p.Offset < 0 {
		return nil, 0, errorf(ErrInvalidRequest, "limit must be 1..%d and offset >= 0", MaxSearchLimit)
	}
	from := `FROM observations o`
	where := []string{"o.deleted_at IS NULL"}
	var args []any
	order, rank := `o.created_at DESC, o.id DESC`, `0.0`
	if strings.TrimSpace(p.Query) != "" {
		match, err := observationMatch(p.Query, p.MatchMode)
		if err != nil {
			return nil, 0, err
		}
		from = `FROM observations_fts f JOIN observations o ON o.id = f.rowid`
		where = append(where, "observations_fts MATCH ?")
		args = append(args, match)
		order, rank = `f.rank`, `f.rank`
	}
	eq := func(col, v string) {
		if v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	in := func(col string, vs []string) {
		if len(vs) > 0 {
			where = append(where, col+" IN (?"+strings.Repeat(", ?", len(vs)-1)+")")
			for _, v := range vs {
				args = append(args, v)
			}
		}
	}
	eq("o.project", p.Project)
	eq("o.scope", p.Scope)
	eq("o.kind", p.Kind)
	eq("o.session_id", p.SessionID)
	in("o.type", p.Types)
	in("o.task_id", p.TaskIDs)
	if p.Since != "" {
		where = append(where, "o.created_at >= ?")
		args = append(args, p.Since)
	}
	if p.Until != "" {
		where = append(where, "o.created_at <= ?")
		args = append(args, p.Until)
	}
	cond := from + ` WHERE ` + strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+observationColumns+`, `+rank+` `+cond+
		` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, p.Limit, p.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var r float64
		obs, err := scanObservation(rows.Scan, &r)
		if err != nil {
			return nil, 0, err
		}
		score := -r
		if score == 0 {
			score = 0
		}
		results = append(results, SearchResult{Observation: obs, Score: score})
	}
	return results, total, rows.Err()
}
