package legacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/rcrespodev/neugram/internal/store"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Check is one of the six provenance verifications of docs/specs/engram-provenance.md.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

var legacyColumns = map[string][]string{
	"sessions": {"id", "project", "directory", "started_at", "ended_at", "summary"},
	"observations": {"id", "sync_id", "session_id", "type", "title", "content", "tool_name", "project", "scope",
		"topic_key", "normalized_hash", "revision_count", "duplicate_count", "last_seen_at", "review_after",
		"pinned", "created_at", "updated_at", "deleted_at"},
	"user_prompts": {"id", "sync_id", "session_id", "content", "project", "created_at"},
	"memory_relations": {"id", "sync_id", "source_id", "target_id", "relation", "reason", "evidence", "confidence",
		"judgment_status", "marked_by_actor", "marked_by_kind", "marked_by_model", "session_id", "superseded_at",
		"superseded_by_relation_id", "created_at", "updated_at"},
}

var legacyTables = []string{"sessions", "observations", "user_prompts", "memory_relations"}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func verify(ctx context.Context, q querier, readOnly error, sum *Summary) []Check {
	schema := checkSchema(ctx, q, readOnly)
	if !schema.OK {
		return []Check{schema}
	}
	return []Check{
		schema,
		countCheck(ctx, q, "2 sync_id no nulo ni duplicado", `
			SELECT (SELECT count(*) FROM legacy.observations WHERE sync_id IS NULL)
			     + (SELECT count(*) - count(DISTINCT sync_id) FROM legacy.observations WHERE sync_id IS NOT NULL)
			     + (SELECT count(*) FROM legacy.user_prompts WHERE sync_id IS NULL)
			     + (SELECT count(*) - count(DISTINCT sync_id) FROM legacy.user_prompts WHERE sync_id IS NOT NULL)`),
		countCheck(ctx, q, "3 session_id con fila en sessions", `
			SELECT (SELECT count(*) FROM legacy.observations o
			         WHERE NOT EXISTS (SELECT 1 FROM legacy.sessions s WHERE s.id = o.session_id))
			     + (SELECT count(*) FROM legacy.user_prompts p
			         WHERE NOT EXISTS (SELECT 1 FROM legacy.sessions s WHERE s.id = p.session_id))`),
		countCheck(ctx, q, "4 relaciones sin extremos colgados", `
			SELECT count(*) FROM legacy.memory_relations r
			 WHERE (r.source_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM legacy.observations o WHERE o.sync_id = r.source_id))
			    OR (r.target_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM legacy.observations o WHERE o.sync_id = r.target_id))`),
		countCheck(ctx, q, "5 scope en el enum y project no nulo", `
			SELECT (SELECT count(*) FROM legacy.observations
			         WHERE scope NOT IN ('project','personal') OR scope IS NULL OR project IS NULL)
			     + (SELECT count(*) FROM legacy.user_prompts WHERE project IS NULL)`),
		checkHashes(ctx, q, sum),
		checkTimestamps(ctx, q, sum),
	}
}

var legacyTimestamps = map[string][]string{
	"sessions":         {"started_at", "ended_at"},
	"observations":     {"last_seen_at", "review_after", "created_at", "updated_at", "deleted_at"},
	"user_prompts":     {"created_at"},
	"memory_relations": {"superseded_at", "created_at", "updated_at"},
}

// checkTimestamps gates the normalization to isoLayout: a value is accepted
// only if it round-trips through SQLite's legacy or ISO layout unchanged.
func checkTimestamps(ctx context.Context, q querier, sum *Summary) Check {
	c := Check{Name: "timestamps normalizables a ISO 8601 UTC"}
	var bad []string
	for _, table := range legacyTables {
		for _, col := range legacyTimestamps[table] {
			var total, invalid int
			err := q.QueryRowContext(ctx, fmt.Sprintf(`
				SELECT count(%[1]s), coalesce(sum(
				         coalesce(strftime('%%Y-%%m-%%d %%H:%%M:%%S', %[1]s) = %[1]s, 0) = 0
				     AND coalesce(strftime('%[3]s', %[1]s) = %[1]s, 0) = 0), 0)
				  FROM legacy.%[2]s WHERE %[1]s IS NOT NULL`, col, table, isoLayout),
			).Scan(&total, &invalid)
			if err != nil {
				c.Detail = err.Error()
				return c
			}
			sum.TimestampsNormalized += total
			sum.TimestampsInvalid += invalid
			if invalid > 0 {
				bad = append(bad, fmt.Sprintf("%s.%s: %d", table, col, invalid))
			}
		}
	}
	c.OK = sum.TimestampsInvalid == 0
	c.Detail = fmt.Sprintf("%s valores · %s no parsean", fmtInt(sum.TimestampsNormalized), fmtInt(sum.TimestampsInvalid))
	if len(bad) > 0 {
		c.Detail += " (" + strings.Join(bad, ", ") + ")"
	}
	return c
}

func checkSchema(ctx context.Context, q querier, readOnly error) Check {
	c := Check{Name: "1 legacy en solo lectura con las cuatro tablas"}
	var missing []string
	for _, table := range legacyTables {
		have := map[string]bool{}
		rows, err := q.QueryContext(ctx, fmt.Sprintf("PRAGMA legacy.table_info(%s)", table))
		if err != nil {
			c.Detail = err.Error()
			return c
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				c.Detail = err.Error()
				return c
			}
			have[name] = true
		}
		rows.Close()
		if len(have) == 0 {
			missing = append(missing, table)
			continue
		}
		for _, col := range legacyColumns[table] {
			if !have[col] {
				missing = append(missing, table+"."+col)
			}
		}
	}
	if len(missing) > 0 {
		c.Detail = "falta " + strings.Join(missing, ", ")
		return c
	}
	if readOnly != nil {
		c.Detail = readOnly.Error()
		return c
	}
	c.OK, c.Detail = true, "escritura rechazada con SQLITE_READONLY; 4 tablas, columnas completas"
	return c
}

func countCheck(ctx context.Context, q querier, name, query string) Check {
	var n int
	if err := q.QueryRowContext(ctx, query).Scan(&n); err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	return Check{Name: name, OK: n == 0, Detail: fmt.Sprintf("%s filas fuera de regla", fmtInt(n))}
}

// checkHashes recomputes every stored hash: the spec asks for a sample, and
// the whole table is cheap enough to make the sample the population.
func checkHashes(ctx context.Context, q querier, sum *Summary) Check {
	c := Check{Name: "6 normalized_hash recomputado"}
	rows, err := q.QueryContext(ctx,
		"SELECT id, content, normalized_hash FROM legacy.observations WHERE normalized_hash IS NOT NULL ORDER BY id")
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	defer rows.Close()
	var first int64
	for rows.Next() {
		var id int64
		var content, hash string
		if err := rows.Scan(&id, &content, &hash); err != nil {
			c.Detail = err.Error()
			return c
		}
		sum.HashChecked++
		if store.NormalizedHash(content) != hash {
			if sum.HashMismatches == 0 {
				first = id
			}
			sum.HashMismatches++
		}
	}
	if err := rows.Err(); err != nil {
		c.Detail = err.Error()
		return c
	}
	c.OK = sum.HashMismatches == 0
	c.Detail = fmt.Sprintf("%s/%s coinciden", fmtInt(sum.HashChecked-sum.HashMismatches), fmtInt(sum.HashChecked))
	if sum.HashMismatches > 0 {
		c.Detail += fmt.Sprintf(" · primera discrepancia en id %d", first)
	}
	return c
}

// probeReadOnly requires a write on the legacy file to fail with
// SQLITE_READONLY. It deletes nothing even where it is allowed to run, and
// runs outside the import transaction: a failed write would poison its commit.
func probeReadOnly(ctx context.Context, q querier) error {
	_, err := q.ExecContext(ctx, "DELETE FROM legacy.sessions WHERE 0")
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_READONLY {
		return nil
	}
	if err == nil {
		return errors.New("legacy acepta escrituras: no está adjunta en solo lectura")
	}
	return fmt.Errorf("sonda de solo lectura: %w", err)
}
