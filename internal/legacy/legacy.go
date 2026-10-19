// Package legacy imports ~/.engram/engram.db into neugram.db, per
// docs/specs/import-legacy.md and docs/specs/engram-provenance.md.
package legacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/rcrespodev/neugram/internal/store"
	_ "modernc.org/sqlite"
)

// ErrVerification means a provenance check failed and nothing was written.
var ErrVerification = errors.New("provenance verification failed")

type Options struct {
	LegacyPath string
	TargetPath string
	DryRun     bool
	Now        func() time.Time
	PathExists func(string) bool
}

// Import verifies the legacy file and copies it into the target in one
// transaction, verification included. A dry run imports into an in-memory
// database and discards it. On any failure the target is left as it was.
func Import(ctx context.Context, opts Options) (*Summary, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PathExists == nil {
		opts.PathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	legacyPath, err := filepath.Abs(opts.LegacyPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return nil, fmt.Errorf("legacy db: %w", err)
	}

	dsn := ":memory:"
	created := false
	if !opts.DryRun {
		if dsn, err = filepath.Abs(opts.TargetPath); err != nil {
			return nil, err
		}
		if sameFile(legacyPath, dsn) {
			return nil, fmt.Errorf("target %s is the legacy db itself", dsn)
		}
		if _, err := os.Stat(dsn); os.IsNotExist(err) {
			created = true
		}
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, err
		}
	}

	sum := &Summary{DryRun: opts.DryRun}
	err = importInto(ctx, dsn, legacyPath, opts, sum)
	if err != nil && created {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			os.Remove(dsn + suffix)
		}
	}
	return sum, err
}

func importInto(ctx context.Context, dsn, legacyPath string, opts Options, sum *Summary) error {
	legacyURI := (&url.URL{Scheme: "file", Path: legacyPath, RawQuery: "mode=ro"}).String()
	conn, closeConn, err := openWithLegacy(ctx, dsn, legacyURI)
	if err != nil {
		return err
	}
	defer closeConn()

	readOnly := probeReadOnly(ctx, conn)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sum.Checks = verify(ctx, tx, readOnly, sum)
	for _, c := range sum.Checks {
		if !c.OK {
			return ErrVerification
		}
	}
	im := &importer{tx: tx, opts: opts, sum: sum, now: opts.Now().UTC().Format(store.TimeLayout)}
	if err := im.run(ctx, legacyPath); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "PRAGMA main.journal_mode = WAL")
	return err
}

func sameFile(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ai, bi)
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(filepath.Dir(b))
	return errA == nil && errB == nil && ra == filepath.Join(rb, filepath.Base(b))
}

func openWithLegacy(ctx context.Context, dsn, legacyURI string) (*sql.Conn, func(), error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	closeAll := func() {
		conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE legacy")
		conn.Close()
		db.Close()
	}
	for _, p := range []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000"} {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS legacy", legacyURI); err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("attach legacy db read-only: %w", err)
	}
	return conn, closeAll, nil
}

type importer struct {
	tx   *sql.Tx
	opts Options
	sum  *Summary
	now  string
}

func (im *importer) run(ctx context.Context, legacyPath string) error {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"defer foreign keys", im.deferForeignKeys},
		{"schema", im.ensureSchema},
		{"memory", im.importMemory},
		{"flow", im.importFlow},
		{"relations", im.importRelations},
		{"fts rebuild", im.rebuildFTS},
		{"foreign key check", im.checkForeignKeys},
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return im.writeMeta(ctx, legacyPath)
}

func (im *importer) exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := im.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (im *importer) deferForeignKeys(ctx context.Context) error {
	_, err := im.exec(ctx, "PRAGMA defer_foreign_keys = ON")
	return err
}

func (im *importer) ensureSchema(ctx context.Context) error {
	var n int
	err := im.tx.QueryRowContext(ctx,
		"SELECT count(*) FROM main.sqlite_master WHERE type = 'table' AND name = 'observations'").Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		if _, err := im.exec(ctx, store.DDL); err != nil {
			return err
		}
		_, err = im.exec(ctx, "INSERT INTO meta(key, value) VALUES ('schema_version', ?)", store.SchemaVersion)
		return err
	}
	var version string
	err = im.tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'schema_version'").Scan(&version)
	if err == sql.ErrNoRows {
		return fmt.Errorf("target has a schema but no meta.schema_version")
	}
	if err != nil {
		return err
	}
	if version != store.SchemaVersion {
		return fmt.Errorf("target schema_version %s, this build expects %s", version, store.SchemaVersion)
	}
	return nil
}

func (im *importer) rebuildFTS(ctx context.Context) error {
	for _, q := range []string{
		"INSERT INTO observations_fts(observations_fts) VALUES('rebuild')",
		"INSERT INTO prompts_fts(prompts_fts) VALUES('rebuild')",
	} {
		if _, err := im.exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (im *importer) checkForeignKeys(ctx context.Context) error {
	rows, err := im.tx.QueryContext(ctx, "PRAGMA main.foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		violations = append(violations, fmt.Sprintf("%s rowid %d → %s", table, rowid.Int64, parent))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(violations) > 0 {
		return fmt.Errorf("%d violations, first: %s", len(violations), violations[0])
	}
	return nil
}

func (im *importer) writeMeta(ctx context.Context, legacyPath string) error {
	for k, v := range map[string]string{"last_import_at": im.now, "legacy_db_path": legacyPath} {
		if _, err := im.exec(ctx,
			"INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", k, v); err != nil {
			return fmt.Errorf("meta %s: %w", k, err)
		}
	}
	return nil
}
