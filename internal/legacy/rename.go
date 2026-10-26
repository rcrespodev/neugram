package legacy

import (
	"context"
	"fmt"
)

// projectRenames maps a legacy project to the one neugram keeps its history
// under; see docs/specs/import-legacy.md (Renombre de projects).
var projectRenames = []struct{ From, To string }{
	{From: "gh-microservices-v2", To: "gh-microservices"},
}

type ProjectRename struct {
	From, To                        string
	Sessions, Observations, Prompts int
	Collisions                      []string
}

func (im *importer) renameProjects(ctx context.Context) error {
	for _, rule := range projectRenames {
		r := ProjectRename{From: rule.From, To: rule.To}
		collisions, err := im.topicCollisions(ctx, rule.From, rule.To)
		if err != nil {
			return err
		}
		r.Collisions = collisions
		for _, t := range []struct {
			table string
			n     *int
		}{{"sessions", &r.Sessions}, {"observations", &r.Observations}, {"user_prompts", &r.Prompts}} {
			n, err := im.exec(ctx, fmt.Sprintf("UPDATE %s SET project = ? WHERE project = ?", t.table), rule.To, rule.From)
			if err != nil {
				return fmt.Errorf("%s: %w", t.table, err)
			}
			*t.n = int(n)
		}
		im.sum.ProjectRenames = append(im.sum.ProjectRenames, r)
	}
	return nil
}

// topicCollisions lists the live (topic_key, scope) pairs present in both
// projects. They are kept as two rows: a later save upserts the newest.
func (im *importer) topicCollisions(ctx context.Context, from, to string) ([]string, error) {
	rows, err := im.tx.QueryContext(ctx, `
		SELECT DISTINCT a.topic_key, a.scope
		  FROM observations a JOIN observations b ON b.topic_key = a.topic_key AND b.scope = a.scope
		 WHERE a.project = ? AND b.project = ? AND a.deleted_at IS NULL AND b.deleted_at IS NULL
		 ORDER BY a.topic_key, a.scope`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key, scope string
		if err := rows.Scan(&key, &scope); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s (%s)", key, scope))
	}
	return out, rows.Err()
}
