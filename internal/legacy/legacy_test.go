package legacy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rcrespodev/neugram/internal/store"
)

const legacySchema = `
CREATE TABLE sessions (id TEXT PRIMARY KEY, project TEXT NOT NULL, directory TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT, summary TEXT);
CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT, session_id TEXT NOT NULL,
  type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL, tool_name TEXT, project TEXT,
  scope TEXT NOT NULL DEFAULT 'project', topic_key TEXT, normalized_hash TEXT,
  revision_count INTEGER NOT NULL DEFAULT 1, duplicate_count INTEGER NOT NULL DEFAULT 1, last_seen_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  deleted_at TEXT, review_after TEXT, expires_at TEXT, embedding BLOB, embedding_model TEXT,
  embedding_created_at TEXT, pinned BOOLEAN NOT NULL DEFAULT 0);
CREATE TABLE user_prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT, session_id TEXT NOT NULL,
  content TEXT NOT NULL, project TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')));
CREATE TABLE memory_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_id TEXT NOT NULL UNIQUE,
  source_id TEXT, target_id TEXT, relation TEXT NOT NULL DEFAULT 'pending', reason TEXT, evidence TEXT,
  confidence REAL, judgment_status TEXT NOT NULL DEFAULT 'pending', marked_by_actor TEXT, marked_by_kind TEXT,
  marked_by_model TEXT, session_id TEXT, superseded_at TEXT, superseded_by_relation_id INTEGER,
  created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')));
`

const (
	sessA    = "11111111-1111-1111-1111-111111111111"
	sessB    = "22222222-2222-2222-2222-222222222222"
	sessNone = "33333333-3333-3333-3333-333333333333"
)

type fixture struct {
	t   *testing.T
	db  *sql.DB
	dir string
	seq int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "engram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, db: db, dir: dir}
	f.exec("PRAGMA synchronous = OFF")
	f.exec(legacySchema)
	f.exec(`INSERT INTO sessions (id, project, directory, started_at) VALUES
		(?, 'repo-a', '/w/a', '2026-09-01 10:00:00'),
		(?, 'repo-b', '/w/b', '2026-09-02 10:00:00'),
		(?, 'repo-a', '/w/a', '2026-09-03 10:00:00'),
		('manual-save-tasks', 'tasks', '/w', '2026-09-01 09:00:00')`, sessA, sessB, sessNone)
	return f
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("%v\n%s", err, q)
	}
}

func (f *fixture) obs(session, project, topicKey, title, content string) string {
	f.seq++
	syncID := fmt.Sprintf("obs-%04d", f.seq)
	var tk any
	if topicKey != "" {
		tk = topicKey
	}
	f.exec(`INSERT INTO observations (sync_id, session_id, type, title, content, project, topic_key, normalized_hash,
		created_at, updated_at) VALUES (?, ?, 'decision', ?, ?, ?, ?, ?, '2026-09-05 10:00:00', '2026-09-05 10:00:00')`,
		syncID, session, title, content, project, tk, store.NormalizedHash(content))
	return syncID
}

func (f *fixture) path() string { return filepath.Join(f.dir, "engram.db") }

const designFooter = "\n\n**Task**: engram-fork-debat — Fork personal de Engram (debate de diseño)"

func (f *fixture) seed() map[string]string {
	ids := map[string]string{}
	ids["design-a"] = f.obs(sessA, "repo-a", "", "Store design", "Store   design\tdecided."+designFooter)
	ids["design-b"] = f.obs(sessB, "repo-b", "", "Import design", "Import design."+designFooter)
	ids["clickup"] = f.obs(sessA, "repo-a", "", "Bug fix", "Fixed it.\n\n**Task**: 869abc · TRIPULSE-3652 — Best Skills")
	ids["none"] = f.obs(sessA, "repo-a", "", "Setup", "Setup.\n\n**Task**: ninguna — exploración")
	ids["unresolved"] = f.obs(sessA, "repo-a", "", "Undecided", "Hmm.\n\n**Task**: pendiente de decidir")
	ids["nofooter"] = f.obs(sessB, "repo-b", "", "Plain", "No footer here.")
	f.exec("UPDATE observations SET deleted_at = '2026-09-06 00:00:00' WHERE sync_id = ?", ids["nofooter"])

	f.obs("manual-save-tasks", "tasks", "task/tripulse-3831/identity", "identity",
		"**Task**: TRIPULSE-3831 — Arreglar suites — de react\n**Product**: Microservices (react) · parent TRIPULSE-3652\n"+
			"**Tracker**: clickup · id 869eqyrxm · https://app.clickup.com/t/869eqyrxm\n\nContexto libre.\n\n"+
			"**Task**: TRIPULSE-3831 — Arreglar suites — de react")
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/identity", "identity",
		"**Task**: engram-fork-debat — Fork personal de Engram (debate de diseño)\n**Product**: engram-fork")
	ids["status"] = f.obs("manual-save-tasks", "tasks", "task/tripulse-3831/status", "status", "review\n\nnotes")
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/status", "status", "pensando")
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/session/"+sessA, "pin", "pinned"+designFooter)
	f.obs("manual-save-tasks", "tasks", "task/none/session/"+sessNone, "pin", "**Task**: ninguna — setup")
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/session/"+sessB, "pin",
		"pinned\n\n**Task**: TRIPULSE-3847 — Pedidos no bajan a SAP")
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/session/99999999-9999-9999-9999-999999999999", "pin", "ghost")
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/wt/neugram-1b", "wt",
		"```json\n{\"path\": \"/w/neugram-1b\", \"repo\": \"/w/neugram\", \"branch\": \"step-1b\", \"base\": \"e4e13f5\","+
			" \"created_by_session\": \"gone-session\", \"purpose\": \"1b\"}\n```"+designFooter)
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/wt/bpall", "wt",
		"**Path**: /w/dead/bpall\n**Repo**: react\n**Branch**: TRIPULSE-3847-BPALL\n**Created by session**: "+sessB)
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/wt/guards", "wt",
		"- Path: /w/dead/guards\n- Repo: integrator\n- Branch: GUARDS\n- Purpose: guards")
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/wt/gone", "wt", "Worktree removido y rama borrada.")
	f.obs("manual-save-tasks", "tasks", "task/tripulse-3847/wt/guards-again", "wt",
		"- Path: /w/dead/guards\n- Repo: integrator\n- Branch: GUARDS-2")
	ids["handoff"] = f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/handoff/"+sessB, "Handoff",
		"**State**: paso 0 hecho."+designFooter)
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/progress/"+sessA+"/impl-2", "Progress",
		"```json\n{\"feature\": \"1b\", \"status\": \"done\", \"references\": {\"files\": [\"a.go:1\"]},"+
			" \"summary\": [\"one\", \"two\"], \"next_steps\": \"single step\"}\n```"+designFooter)
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/progress/"+sessA+"/explore-1", "Progress", "not json")
	ids["control-other"] = f.obs("manual-save-tasks", "tasks", "architecture/engram-tasks-design", "design", "free text")
	ids["misfiled"] = f.obs(sessA, "tasks", "", "Misfiled memory", "**What**: real memory."+designFooter)

	f.exec(`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES
		('prompt-1', ?, 'quiero el import', 'repo-a'), ('prompt-2', ?, 'y el doctor', 'repo-b')`, sessA, sessB)
	f.exec(`INSERT INTO memory_relations (sync_id, source_id, target_id, relation, judgment_status, session_id)
		VALUES ('rel-1', ?, ?, 'related', 'judged', ?), ('rel-2', ?, ?, 'pending', 'pending', NULL),
		       ('rel-3', ?, ?, 'related', 'judged', NULL), ('rel-4', ?, ?, 'related', 'judged', NULL)`,
		ids["design-a"], ids["design-b"], sessA, ids["design-a"], ids["handoff"], ids["design-a"], ids["control-other"],
		ids["design-a"], ids["status"])
	return ids
}

func runImport(t *testing.T, legacyPath, target string, dryRun bool) *Summary {
	t.Helper()
	sum, err := Import(context.Background(), Options{
		LegacyPath: legacyPath, TargetPath: target, DryRun: dryRun,
		PathExists: func(p string) bool { return p == "/w/neugram-1b" },
	})
	if err != nil {
		t.Fatalf("import: %v (checks %+v)", err, sum)
	}
	return sum
}

func openTarget(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func scalar[T any](t *testing.T, db *sql.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
	return v
}

func TestImportCopiesMemoryAndFlow(t *testing.T) {
	f := newFixture(t)
	ids := f.seed()
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum := runImport(t, f.path(), target, false)
	db := openTarget(t, target)

	for _, c := range sum.Checks {
		if !c.OK {
			t.Fatalf("check failed: %+v", c)
		}
	}
	if got := scalar[int](t, db, "SELECT count(*) FROM sessions"); got != 4 {
		t.Errorf("sessions = %d", got)
	}
	if got := scalar[int](t, db, "SELECT count(*) FROM observations WHERE kind IS NULL"); got != 8 {
		t.Errorf("plain observations = %d", got)
	}
	if got := scalar[string](t, db, `SELECT group_concat(o.title || ' ' || o.type || ' ' || o.created_at || ' ' || coalesce(t.key, '-'), ' | ')
		FROM (SELECT * FROM observations WHERE project = 'tasks' ORDER BY id) o LEFT JOIN tasks t ON t.id = o.task_id`); got != "design decision 2026-09-05T10:00:00Z - | Misfiled memory decision 2026-09-05T10:00:00Z engram-fork-debat" {
		t.Errorf("plain tasks-project observations = %q", got)
	}
	if got := scalar[string](t, db, "SELECT deleted_at FROM observations WHERE sync_id = ?", ids["nofooter"]); got == "" {
		t.Error("soft delete not copied")
	}

	if got := scalar[int](t, db, `SELECT count(DISTINCT o.project) FROM observations o JOIN tasks t ON t.id = o.task_id
		WHERE t.key = 'ENGRAM-FORK-DEBAT' COLLATE NOCASE AND o.kind IS NULL`); got != 3 {
		t.Errorf("design observations across repos = %d", got)
	}
	if got := scalar[string](t, db, `SELECT t.key FROM observations o JOIN tasks t ON t.id = o.task_id
		WHERE o.sync_id = ?`, ids["clickup"]); got != "TRIPULSE-3652" {
		t.Errorf("clickup footer key = %q", got)
	}
	if got := scalar[int](t, db, "SELECT count(*) FROM observations WHERE sync_id IN (?, ?) AND task_id IS NULL",
		ids["none"], ids["unresolved"]); got != 2 {
		t.Errorf("none/unresolved with task = %d", 2-got)
	}

	var title, status, product, notes, tracker string
	if err := db.QueryRow(`SELECT t.title, t.status, p.name, t.notes, t.tracker_kind || ' ' || t.tracker_id || ' ' || t.tracker_url
		FROM tasks t JOIN products p ON p.id = t.product_id WHERE t.key = 'TRIPULSE-3831'`).
		Scan(&title, &status, &product, &notes, &tracker); err != nil {
		t.Fatal(err)
	}
	if title != "Arreglar suites — de react" || status != "review" || product != "tripulse" {
		t.Errorf("task = %q %q %q", title, status, product)
	}
	if tracker != "clickup 869eqyrxm https://app.clickup.com/t/869eqyrxm" {
		t.Errorf("tracker = %q", tracker)
	}
	if !strings.Contains(notes, "[import-legacy] **Product** original: Microservices (react) · parent TRIPULSE-3652") || !strings.Contains(notes, "Contexto libre.") {
		t.Errorf("notes = %q", notes)
	}
	if got := scalar[string](t, db, `SELECT p.key FROM tasks t JOIN tasks p ON p.id = t.parent_task_id WHERE t.key = 'TRIPULSE-3831'`); got != "TRIPULSE-3652" {
		t.Errorf("parent of TRIPULSE-3831 = %q", got)
	}
	if got := scalar[string](t, db, "SELECT status FROM tasks WHERE key = 'engram-fork-debat'"); got != "todo" {
		t.Errorf("non-enum status = %q", got)
	}
	if got := scalar[string](t, db, "SELECT title FROM tasks WHERE key = 'TRIPULSE-3847'"); got != "TRIPULSE-3847" {
		t.Errorf("task without identity title = %q", got)
	}

	if got := scalar[string](t, db, `SELECT t.key || ' ' || s.pinned_by || ' ' || (s.pinned_at = s.created_at)
		FROM sessions s JOIN tasks t ON t.id = s.task_id WHERE s.id = ?`, sessA); got != "engram-fork-debat import 1" {
		t.Errorf("pin = %q", got)
	}
	if got := scalar[int](t, db, "SELECT count(*) FROM sessions WHERE id = ? AND task_id IS NULL AND pinned_at IS NOT NULL",
		sessNone); got != 1 {
		t.Error("pin to none not recorded")
	}

	if got := scalar[int](t, db, "SELECT count(*) FROM worktrees"); got != 3 {
		t.Errorf("worktrees = %d", got)
	}
	if got := scalar[string](t, db, "SELECT repo || ' ' || branch || ' ' || created_by_session FROM worktrees WHERE path = '/w/dead/bpall'"); got != "react TRIPULSE-3847-BPALL "+sessB {
		t.Errorf("legacy wt = %q", got)
	}

	if got := scalar[string](t, db, `SELECT o.project || ' ' || o.session_id || ' ' || t.key FROM observations o
		JOIN tasks t ON t.id = o.task_id WHERE o.kind = 'handoff'`); got != "repo-b "+sessB+" engram-fork-debat" {
		t.Errorf("handoff = %q", got)
	}
	if got := scalar[string](t, db, "SELECT role || ' ' || status || ' ' || summary || ' ' || next_steps FROM progress_reports"); got != `impl done ["one","two"] ["single step"]` {
		t.Errorf("progress = %q", got)
	}

	if got := scalar[int](t, db, "SELECT count(*) FROM memory_relations"); got != 3 {
		t.Errorf("relations = %d", got)
	}
	if got := scalar[int](t, db, "SELECT count(*) FROM user_prompts"); got != 2 {
		t.Errorf("prompts = %d", got)
	}
	if got := scalar[int](t, db, `SELECT count(*) FROM observations_fts WHERE observations_fts MATCH '"design"'`); got != 3 {
		t.Errorf("fts hits = %d", got)
	}
	if got := scalar[int](t, db, `SELECT count(*) FROM prompts_fts WHERE prompts_fts MATCH '"doctor"'`); got != 1 {
		t.Errorf("prompt fts hits = %d", got)
	}
	if _, err := db.Exec("INSERT INTO observations_fts(observations_fts, rank) VALUES('integrity-check', 1)"); err != nil {
		t.Errorf("fts integrity: %v", err)
	}
	if got := scalar[string](t, db, "SELECT value FROM meta WHERE key = 'legacy_db_path'"); got != f.path() {
		t.Errorf("meta legacy_db_path = %q", got)
	}

	want := map[string]int{"sessions": 4, "observations": 6, "control": 18, "prompts": 2, "relations": 3,
		"judged": 2, "pending": 1, "tasks": 4, "identity": 2, "products": 2, "pins": 4, "pinsTask": 2, "pinsNone": 1,
		"worktrees": 3, "alive": 1, "dead": 2, "handoffs": 1, "progress": 1, "footers": 6, "footersTask": 4,
		"footersNone": 1, "hash": 24, "plain": 2, "overwritten": 1, "epics": 3}
	got := map[string]int{"sessions": sum.Sessions, "observations": sum.Observations, "control": sum.ControlRecords,
		"prompts": sum.Prompts, "relations": sum.Relations, "judged": sum.RelationsJudged,
		"pending": sum.RelationsPending, "tasks": sum.Tasks, "identity": sum.TasksWithIdentity,
		"products": sum.Products, "pins": sum.Pins, "pinsTask": sum.PinsWithTask, "pinsNone": sum.PinsNone,
		"worktrees": sum.Worktrees, "alive": sum.WorktreesAlive, "dead": sum.WorktreesDead,
		"handoffs": sum.Handoffs, "progress": sum.Progress, "footers": sum.Footers,
		"footersTask": sum.FootersWithTask, "footersNone": sum.FootersNone, "hash": sum.HashChecked,
		"overwritten": sum.WorktreesOverwritten, "plain": len(sum.PlainImported), "epics": sum.Epics}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("summary %s = %d, want %d", k, got[k], v)
		}
	}
	if strings.Join(sum.TasksWithoutIdentity, ",") != "TRIPULSE-3847,TRIPULSE-3652" {
		t.Errorf("without identity = %v", sum.TasksWithoutIdentity)
	}
	if sum.ParentsFromHint != 1 || len(sum.PinsMissingSession) != 1 || len(sum.WorktreesUnparseable) != 1 ||
		len(sum.ProgressSkipped) != 1 || len(sum.FootersUnresolved) != 1 || len(sum.RelationsSkipped) != 1 ||
		len(sum.StatusWarnings) != 1 || sum.RelationsSkipped[0] != "rel-4" {
		t.Errorf("lists = %+v", sum)
	}
}

var dumpedTables = []string{"sessions", "observations", "user_prompts", "memory_relations", "products", "tasks",
	"worktrees", "progress_reports"}

func dump(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	queries := []string{"SELECT key, value FROM meta WHERE key != 'last_import_at' ORDER BY key"}
	for _, table := range dumpedTables {
		queries = append(queries, "SELECT * FROM "+table+" ORDER BY 1")
	}
	for _, q := range queries {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", q, vals)
		}
		rows.Close()
	}
	return b.String()
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestImportTwiceGivesSameContent(t *testing.T) {
	f := newFixture(t)
	f.seed()
	target := filepath.Join(t.TempDir(), "neugram.db")
	runImport(t, f.path(), target, false)
	db := openTarget(t, target)
	first := dump(t, db)
	runImport(t, f.path(), target, false)
	if second := dump(t, db); second != first {
		t.Errorf("second run changed the target content")
	}
	if _, err := db.Exec("INSERT INTO observations_fts(observations_fts, rank) VALUES('integrity-check', 1)"); err != nil {
		t.Errorf("fts integrity after rerun: %v", err)
	}
}

func TestImportLeavesLegacyFileUnchanged(t *testing.T) {
	f := newFixture(t)
	f.seed()
	before := fileSum(t, f.path())
	runImport(t, f.path(), filepath.Join(t.TempDir(), "neugram.db"), false)
	if fileSum(t, f.path()) != before {
		t.Error("legacy file changed")
	}
}

func TestFailureMidImportRollsBackEverything(t *testing.T) {
	f := newFixture(t)
	ids := f.seed()
	target := filepath.Join(t.TempDir(), "neugram.db")
	runImport(t, f.path(), target, false)
	db := openTarget(t, target)
	before := dump(t, db)

	f.exec("UPDATE observations SET content = 'changed' || content, normalized_hash = NULL WHERE sync_id = ?", ids["design-a"])
	f.exec(`INSERT INTO memory_relations (sync_id, source_id, target_id, session_id) VALUES ('rel-bad', ?, ?, 'no-such-session')`,
		ids["design-a"], ids["design-b"])
	_, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: target})
	if err == nil || !strings.Contains(err.Error(), "foreign key check") {
		t.Fatalf("err = %v", err)
	}
	if after := dump(t, db); after != before {
		t.Error("failed import left partial writes")
	}

	fresh := filepath.Join(t.TempDir(), "fresh.db")
	if _, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: fresh}); err == nil {
		t.Fatal("import with a dangling relation session succeeded")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("failed import left the target it created: %v", err)
	}
}

func TestTargetSameAsLegacyIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seed()
	before := fileSum(t, f.path())
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(f.path(), link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{f.path(), link} {
		_, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: target})
		if err == nil || !strings.Contains(err.Error(), "is the legacy db itself") {
			t.Errorf("target %s: err = %v", target, err)
		}
	}
	if fileSum(t, f.path()) != before {
		t.Error("legacy file changed")
	}
}

func TestReadOnlyProbeRejectsWritableLegacy(t *testing.T) {
	f := newFixture(t)
	f.seed()
	before := fileSum(t, f.path())
	db := openTarget(t, ":memory:")
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("ATTACH DATABASE ? AS legacy", f.path()); err != nil {
		t.Fatal(err)
	}
	err := probeReadOnly(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "acepta escrituras") {
		t.Errorf("probe = %v", err)
	}
	if c := checkSchema(context.Background(), db, err); c.OK {
		t.Errorf("check = %+v", c)
	}
	if fileSum(t, f.path()) != before {
		t.Error("probe changed the legacy file")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.seed()
	target := filepath.Join(t.TempDir(), "sub", "neugram.db")
	sum := runImport(t, f.path(), target, true)
	if sum.Observations != 6 || sum.Handoffs != 1 {
		t.Errorf("dry run summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Errorf("dry run touched the target dir: %v", err)
	}
}

func TestHashMismatchAbortsBeforeWriting(t *testing.T) {
	f := newFixture(t)
	ids := f.seed()
	f.exec("UPDATE observations SET normalized_hash = 'deadbeef' WHERE sync_id = ?", ids["design-b"])
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: target})
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("err = %v", err)
	}
	if sum.HashMismatches != 1 || sum.Checks[5].OK {
		t.Errorf("checks = %+v", sum.Checks)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("aborted import created the target: %v", err)
	}
}

func TestOrphanSessionAborts(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.obs("no-such-session", "repo-a", "", "orphan", "orphan")
	_, err := Import(context.Background(), Options{LegacyPath: f.path(), DryRun: true})
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingLegacyTableAborts(t *testing.T) {
	f := newFixture(t)
	f.exec("DROP TABLE memory_relations")
	sum, err := Import(context.Background(), Options{LegacyPath: f.path(), DryRun: true})
	if !errors.Is(err, ErrVerification) || !strings.Contains(sum.Checks[0].Detail, "memory_relations") {
		t.Fatalf("err = %v, checks = %+v", err, sum.Checks)
	}
}

func TestSummaryWritesSpecLines(t *testing.T) {
	f := newFixture(t)
	f.seed()
	sum := runImport(t, f.path(), "", true)
	var b strings.Builder
	sum.Write(&b)
	for _, want := range []string{"sesiones", "observaciones", "(15 registros de control", "tasks→obs", "pies", "hash",
		"Microservices (react) · parent TRIPULSE-3652 → tripulse"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("summary misses %q:\n%s", want, b.String())
		}
	}
}

func TestImportNormalizesTimestampsAndWritesSchemaVersion(t *testing.T) {
	f := newFixture(t)
	ids := f.seed()
	f.exec("UPDATE observations SET deleted_at = '2026-09-06T01:02:03Z' WHERE sync_id = ?", ids["nofooter"])
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum := runImport(t, f.path(), target, false)
	db := openTarget(t, target)

	if got := scalar[string](t, db, "SELECT value FROM meta WHERE key = 'schema_version'"); got != "1" {
		t.Errorf("schema_version = %q", got)
	}
	if got := scalar[string](t, db, "SELECT created_at FROM observations WHERE sync_id = ?", ids["design-a"]); got != "2026-09-05T10:00:00Z" {
		t.Errorf("created_at = %q", got)
	}
	if got := scalar[string](t, db, "SELECT deleted_at FROM observations WHERE sync_id = ?", ids["nofooter"]); got != "2026-09-06T01:02:03Z" {
		t.Errorf("deleted_at = %q", got)
	}
	if got := scalar[string](t, db, "SELECT started_at || ' ' || pinned_at FROM sessions WHERE id = ?", sessA); got != "2026-09-01T10:00:00Z 2026-09-01T10:00:00Z" {
		t.Errorf("session = %q", got)
	}
	const iso = "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z"
	for _, q := range []string{
		"SELECT count(*) FROM sessions WHERE NOT (started_at GLOB ?1 AND created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM observations WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1) OR deleted_at NOT GLOB ?1",
		"SELECT count(*) FROM user_prompts WHERE created_at NOT GLOB ?1",
		"SELECT count(*) FROM memory_relations WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM tasks WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM worktrees WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM progress_reports WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM products WHERE NOT (created_at GLOB ?1 AND updated_at GLOB ?1)",
		"SELECT count(*) FROM meta WHERE key = 'last_import_at' AND value NOT GLOB ?1",
	} {
		if got := scalar[int](t, db, q, iso); got != 0 {
			t.Errorf("%d rows not ISO: %s", got, q)
		}
	}
	if sum.TimestampsInvalid != 0 || sum.TimestampsNormalized == 0 {
		t.Errorf("timestamps = %d/%d", sum.TimestampsNormalized, sum.TimestampsInvalid)
	}
}

func TestUnparseableTimestampAbortsBeforeWriting(t *testing.T) {
	f := newFixture(t)
	ids := f.seed()
	f.exec("UPDATE observations SET review_after = 'next week' WHERE sync_id = ?", ids["design-a"])
	f.exec("UPDATE sessions SET ended_at = '2026-13-40 10:00:00' WHERE id = ?", sessB)
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: target})
	if !errors.Is(err, ErrVerification) {
		t.Fatalf("err = %v", err)
	}
	last := sum.Checks[len(sum.Checks)-1]
	if sum.TimestampsInvalid != 2 || last.OK || !strings.Contains(last.Detail, "observations.review_after: 1") {
		t.Errorf("timestamps check = %+v", last)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("aborted import created the target: %v", err)
	}
}

func TestTargetWithOtherSchemaVersionIsRejected(t *testing.T) {
	f := newFixture(t)
	f.seed()
	target := filepath.Join(t.TempDir(), "neugram.db")
	runImport(t, f.path(), target, false)
	db := openTarget(t, target)
	if _, err := db.Exec("UPDATE meta SET value = '2' WHERE key = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	_, err := Import(context.Background(), Options{LegacyPath: f.path(), TargetPath: target})
	if err == nil || !strings.Contains(err.Error(), "schema_version 2") {
		t.Fatalf("err = %v", err)
	}
}

func identityContent(key, product string) string {
	return "**Task**: " + key + " — título\n**Product**: " + product
}

func TestImportBuildsHierarchyFromHintsAndProductRule(t *testing.T) {
	f := newFixture(t)
	f.seed()
	for key, product := range map[string]string{
		"TRIPULSE-3945": "Integración de socios de negocio (integrator, react)",
		"TRIPULSE-3947": "Integración de socios de negocio (integrator) · subtarea de TRIPULSE-3945",
		"TRIPULSE-3963": "TRIPULSE-3945 Socios de negocio",
		"TRIPULSE-3962": "Socios de negocio: SAP → GCP → canales (TRIPULSE-3945)",
		"TRIPULSE-3909": "Soporte Chile / Integración SAP-Magento (subtarea de 869eknv3x, lista MS Sprint 7)",
		"TRIPULSE-3500": "Microservices",
		"CHAIN-A":       "Microservices (padre CHAIN-B)",
		"CHAIN-B":       "Microservices (padre CHAIN-C)",
		"CHAIN-C":       "Microservices (padre CHAIN-D)",
		"CHAIN-D":       "Microservices",
	} {
		f.obs("manual-save-tasks", "tasks", "task/"+strings.ToLower(key)+"/identity", "identity", identityContent(key, product))
	}
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum := runImport(t, f.path(), target, false)
	db := openTarget(t, target)

	parents := map[string]string{}
	rows, err := db.Query(`SELECT t.key, coalesce(p.key, '') FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_task_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, p string
		if err := rows.Scan(&k, &p); err != nil {
			t.Fatal(err)
		}
		parents[k] = p
	}
	rows.Close()
	want := map[string]string{
		"TRIPULSE-3945": "", "TRIPULSE-3947": "TRIPULSE-3945", "TRIPULSE-3963": "TRIPULSE-3945",
		"TRIPULSE-3962": "TRIPULSE-3945", "TRIPULSE-3909": "", "TRIPULSE-3500": "",
		"CHAIN-A": "", "CHAIN-B": "CHAIN-C", "CHAIN-C": "CHAIN-D", "CHAIN-D": "",
	}
	for k, w := range want {
		if parents[k] != w {
			t.Errorf("parent of %s = %q, want %q", k, parents[k], w)
		}
	}
	if sum.ParentsFromHint != 4 || sum.ParentsFromRule != 2 {
		t.Errorf("from hint %d, from rule %d", sum.ParentsFromHint, sum.ParentsFromRule)
	}
	if strings.Join(sum.ParentHintsUnresolved, ",") != "TRIPULSE-3909 → 869eknv3x" {
		t.Errorf("unresolved hints = %v", sum.ParentHintsUnresolved)
	}
	if strings.Join(sum.ParentsDropped, ",") != "CHAIN-A → CHAIN-B" {
		t.Errorf("dropped = %v", sum.ParentsDropped)
	}
}
