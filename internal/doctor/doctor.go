// Package doctor checks the health of neugram.db, per the doctor entry of docs/specs/cli.md.
package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rcrespodev/neugram/internal/store"
	_ "modernc.org/sqlite"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "FAIL"
	Skip Status = "skip"
)

type Result struct {
	Name   string
	Status Status
	Detail string
}

// Run never creates the database: a missing file is a failed check.
func Run(ctx context.Context, path string) ([]Result, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return []Result{{"base", Fail, err.Error()}}, nil
	}
	size := info.Size()
	if wal, err := os.Stat(abs + "-wal"); err == nil {
		size += wal.Size()
	}

	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=rw"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	results := []Result{{"base", OK, fmt.Sprintf("%s · %s", abs, humanBytes(size))}}
	var tables int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('observations','meta','tasks','gates')").
		Scan(&tables); err != nil {
		return append(results, Result{"schema", Fail, err.Error()}), nil
	}
	if tables < 4 {
		return append(results, Result{"schema", Fail, "la base no tiene el DDL de neugram"}), nil
	}
	results = append(results,
		schemaVersionCheck(ctx, db),
		ftsCheck(ctx, db, "observations_fts"),
		ftsCheck(ctx, db, "prompts_fts"),
		gatesCheck(ctx, db),
		lastImportCheck(ctx, db),
		parentCheck(ctx, db),
		timestampsCheck(ctx, db),
		Result{"daemon", Skip, "serve llega en el paso 2 del roadmap; nada que chequear en :7438"},
	)
	return results, nil
}

// Failed reports whether any check failed.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

func Write(w io.Writer, results []Result) {
	for _, r := range results {
		fmt.Fprintf(w, "%-4s  %-22s %s\n", r.Status, r.Name, r.Detail)
	}
}

// ftsCheck runs FTS5 integrity-check with rank 1, which also compares the
// index against its external content table.
func ftsCheck(ctx context.Context, db *sql.DB, table string) Result {
	name := "fts " + table
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %[1]s(%[1]s, rank) VALUES('integrity-check', 1)", table)); err != nil {
		return Result{name, Fail, err.Error()}
	}
	var docs int
	source := strings.TrimSuffix(table, "_fts")
	if source == "prompts" {
		source = "user_prompts"
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+source).Scan(&docs); err != nil {
		return Result{name, Fail, err.Error()}
	}
	return Result{name, OK, fmt.Sprintf("consistente con %s (%d filas)", source, docs)}
}

func schemaVersionCheck(ctx context.Context, db *sql.DB) Result {
	const name = "schema_version"
	var v string
	err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err == sql.ErrNoRows {
		return Result{name, Fail, "meta no tiene schema_version"}
	}
	if err != nil {
		return Result{name, Fail, err.Error()}
	}
	if v != store.SchemaVersion {
		return Result{name, Fail, fmt.Sprintf("%s, este binario espera %s", v, store.SchemaVersion)}
	}
	return Result{name, OK, v}
}

func gatesCheck(ctx context.Context, db *sql.DB) Result {
	rows, err := db.QueryContext(ctx, "SELECT name FROM gates WHERE enabled = 1 ORDER BY name")
	if err != nil {
		return Result{"gates", Fail, err.Error()}
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return Result{"gates", Fail, err.Error()}
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return Result{"gates", Fail, err.Error()}
	}
	if len(names) == 0 {
		return Result{"gates", Warn, "ninguno activo; los gates default llegan en el paso 3"}
	}
	return Result{"gates", OK, fmt.Sprintf("%d activos: %s", len(names), strings.Join(names, ", "))}
}

func lastImportCheck(ctx context.Context, db *sql.DB) Result {
	var at, from sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT (SELECT value FROM meta WHERE key = 'last_import_at'),
		       (SELECT value FROM meta WHERE key = 'legacy_db_path')`).Scan(&at, &from)
	if err != nil {
		return Result{"última importación", Fail, err.Error()}
	}
	if !at.Valid {
		return Result{"última importación", Warn, "nunca"}
	}
	return Result{"última importación", OK, fmt.Sprintf("%s desde %s", at.String, from.String)}
}

var timestampColumns = []struct {
	table   string
	columns []string
}{
	{"sessions", []string{"started_at", "ended_at", "pinned_at", "created_at", "updated_at"}},
	{"observations", []string{"last_seen_at", "review_after", "created_at", "updated_at", "deleted_at"}},
	{"user_prompts", []string{"created_at"}},
	{"memory_relations", []string{"superseded_at", "created_at", "updated_at"}},
	{"products", []string{"created_at", "updated_at"}},
	{"tasks", []string{"created_at", "updated_at"}},
	{"worktrees", []string{"created_at", "updated_at"}},
	{"progress_reports", []string{"created_at", "updated_at"}},
}

// isoGlob matches the store's timestamp layout, 2006-01-02T15:04:05Z.
const isoGlob = "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z"

func timestampsCheck(ctx context.Context, db *sql.DB) Result {
	const name = "timestamps"
	var bad []string
	total := 0
	for _, tc := range timestampColumns {
		for _, col := range tc.columns {
			var n int
			q := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s IS NOT NULL AND %[2]s NOT GLOB ?", tc.table, col)
			if err := db.QueryRowContext(ctx, q, isoGlob).Scan(&n); err != nil {
				return Result{name, Fail, err.Error()}
			}
			if n > 0 {
				total += n
				bad = append(bad, fmt.Sprintf("%s.%s: %d", tc.table, col, n))
			}
		}
	}
	var meta int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM meta WHERE key = 'last_import_at' AND value NOT GLOB ?", isoGlob).Scan(&meta); err != nil {
		return Result{name, Fail, err.Error()}
	}
	if meta > 0 {
		total += meta
		bad = append(bad, fmt.Sprintf("meta.last_import_at: %d", meta))
	}
	if total > 0 {
		return Result{name, Fail, fmt.Sprintf("%d valores fuera de 2006-01-02T15:04:05Z (%s)", total, strings.Join(bad, ", "))}
	}
	return Result{name, OK, "todos en 2006-01-02T15:04:05Z"}
}

func parentCheck(ctx context.Context, db *sql.DB) Result {
	rows, err := db.QueryContext(ctx, `
		SELECT t.key FROM tasks t
		 WHERE t.parent_task_id IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM tasks p WHERE p.id = t.parent_task_id)
		 ORDER BY t.key`)
	if err != nil {
		return Result{"parent_task_id", Fail, err.Error()}
	}
	defer rows.Close()
	var broken []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return Result{"parent_task_id", Fail, err.Error()}
		}
		broken = append(broken, k)
	}
	if err := rows.Err(); err != nil {
		return Result{"parent_task_id", Fail, err.Error()}
	}
	if len(broken) > 0 {
		return Result{"parent_task_id", Fail, "padre inexistente en: " + strings.Join(broken, ", ")}
	}
	return Result{"parent_task_id", OK, "sin padres rotos"}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
