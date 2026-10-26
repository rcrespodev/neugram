package store

import "context"

// GateRow is a row of the gates table: an override of the in-code defaults.
type GateRow struct {
	Name    string
	Enabled bool
	Params  string
}

func (s *Store) ListGates(ctx context.Context) ([]GateRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, enabled, COALESCE(params, '') FROM gates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GateRow
	for rows.Next() {
		var g GateRow
		var enabled int
		if err := rows.Scan(&g.Name, &enabled, &g.Params); err != nil {
			return nil, err
		}
		g.Enabled = enabled != 0
		out = append(out, g)
	}
	return out, rows.Err()
}
