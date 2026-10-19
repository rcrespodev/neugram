package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/rcrespodev/neugram/internal/store"
)

func newDB(t *testing.T, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neugram.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := []string{"PRAGMA synchronous = OFF", store.DDL,
		"INSERT INTO meta (key, value) VALUES ('schema_version', '" + store.SchemaVersion + "')"}
	for _, s := range append(setup, stmts...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return path
}

func byName(t *testing.T, path string) map[string]Result {
	t.Helper()
	results, err := Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Result{}
	for _, r := range results {
		m[r.Name] = r
	}
	return m
}

const sessionAndObservation = `
INSERT INTO sessions (id, project, directory, started_at, created_at, updated_at)
VALUES ('s1', 'p', '/d', '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z');
INSERT INTO observations (sync_id, session_id, type, title, content, project, created_at, updated_at)
VALUES ('obs-1', 's1', 'decision', 'title', 'content', 'p', '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z');`

func TestHealthyImportedDatabase(t *testing.T) {
	path := newDB(t, sessionAndObservation,
		`INSERT INTO meta (key, value) VALUES ('last_import_at', '2026-10-19T12:00:00Z'), ('legacy_db_path', '/e.db')`)
	results, err := Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if Failed(results) {
		t.Fatalf("results = %+v", results)
	}
	m := byName(t, path)
	if m["última importación"].Status != OK || m["gates"].Status != Warn || m["daemon"].Status != Skip {
		t.Errorf("results = %+v", m)
	}
}

func TestMissingDatabaseFailsWithoutCreatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "neugram.db")
	results, err := Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !Failed(results) {
		t.Errorf("results = %+v", results)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("doctor created the database: %v", err)
	}
}

func TestDatabaseWithoutSchemaFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec("CREATE TABLE x (a)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if m := byName(t, path); m["schema"].Status != Fail {
		t.Errorf("results = %+v", m)
	}
}

func TestStaleFTSIndexFails(t *testing.T) {
	path := newDB(t, "DROP TRIGGER obs_fts_insert", sessionAndObservation)
	m := byName(t, path)
	if m["fts observations_fts"].Status != Fail || m["fts prompts_fts"].Status != OK {
		t.Errorf("results = %+v", m)
	}
}

func TestBrokenParentFails(t *testing.T) {
	path := newDB(t, "PRAGMA foreign_keys = OFF",
		`INSERT INTO tasks (id, key, title, parent_task_id, created_at, updated_at) VALUES ('t1', 'K-1', 'k', 'gone', '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z')`)
	if m := byName(t, path); m["parent_task_id"].Status != Fail {
		t.Errorf("results = %+v", m)
	}
}

func TestActiveGatesAreListed(t *testing.T) {
	path := newDB(t, `INSERT INTO gates (name, enabled, updated_at) VALUES ('require-pin', 1, 't'), ('off', 0, 't')`)
	if g := byName(t, path)["gates"]; g.Status != OK || g.Detail != "1 activos: require-pin" {
		t.Errorf("gates = %+v", g)
	}
}

func TestLegacyTimestampFormatFails(t *testing.T) {
	path := newDB(t, sessionAndObservation, "UPDATE observations SET last_seen_at = '2026-05-05 19:31:40'")
	r := byName(t, path)["timestamps"]
	if r.Status != Fail || r.Detail != "1 valores fuera de 2006-01-02T15:04:05Z (observations.last_seen_at: 1)" {
		t.Errorf("timestamps = %+v", r)
	}
}

func TestSchemaVersionMismatchFails(t *testing.T) {
	path := newDB(t, "UPDATE meta SET value = '0' WHERE key = 'schema_version'")
	if r := byName(t, path)["schema_version"]; r.Status != Fail {
		t.Errorf("schema_version = %+v", r)
	}
	path = newDB(t, "DELETE FROM meta WHERE key = 'schema_version'")
	if r := byName(t, path)["schema_version"]; r.Status != Fail || r.Detail != "meta no tiene schema_version" {
		t.Errorf("schema_version = %+v", r)
	}
}
