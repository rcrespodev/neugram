package legacy

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	sessOld = "44444444-4444-4444-4444-444444444444"
	sessNew = "55555555-5555-5555-5555-555555555555"
)

func renameFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.exec(`INSERT INTO sessions (id, project, directory, started_at) VALUES
		(?, 'gh-microservices-v2', '/w/v2', '2026-09-01 10:00:00'),
		(?, 'gh-microservices', '/w/gh', '2026-09-02 10:00:00')`, sessOld, sessNew)
	f.obs(sessOld, "gh-microservices-v2", "arch/x", "Old x", "old x")
	f.obs(sessOld, "gh-microservices-v2", "arch/only-old", "Only old", "only old")
	f.obs(sessOld, "gh-microservices-v2", "", "No topic", "no topic")
	f.obs(sessNew, "gh-microservices", "arch/x", "New x", "new x")
	deleted := f.obs(sessOld, "gh-microservices-v2", "arch/gone", "Gone old", "gone old")
	f.obs(sessNew, "gh-microservices", "arch/gone", "Gone new", "gone new")
	f.exec("UPDATE observations SET deleted_at = '2026-09-06 00:00:00' WHERE sync_id = ?", deleted)
	f.obs("manual-save-tasks", "tasks", "task/engram-fork-debat/handoff/"+sessOld, "Handoff", "**State**: hecho."+designFooter)
	f.exec(`INSERT INTO user_prompts (sync_id, session_id, content, project) VALUES
		('prompt-old', ?, 'prompt viejo', 'gh-microservices-v2'), ('prompt-new', ?, 'prompt nuevo', 'gh-microservices')`, sessOld, sessNew)
	return f
}

func TestImportRenamesProjectInEveryTable(t *testing.T) {
	f := renameFixture(t)
	target := filepath.Join(t.TempDir(), "neugram.db")
	sum := runImport(t, f.path(), target, false)
	db := openTarget(t, target)

	for _, table := range []string{"sessions", "observations", "user_prompts"} {
		if n := scalar[int](t, db, "SELECT count(*) FROM "+table+" WHERE project = 'gh-microservices-v2'"); n != 0 {
			t.Errorf("%s keeps %d rows in the old project", table, n)
		}
	}
	if got := scalar[string](t, db, "SELECT project FROM observations WHERE topic_key LIKE 'task/%/handoff/' || ?", sessOld); got != "gh-microservices" {
		t.Errorf("handoff of a renamed session has project %q", got)
	}
	if len(sum.ProjectRenames) != 1 {
		t.Fatalf("got %d renames, want 1", len(sum.ProjectRenames))
	}
	r := sum.ProjectRenames[0]
	if r.From != "gh-microservices-v2" || r.To != "gh-microservices" || r.Sessions != 1 || r.Observations != 4 || r.Prompts != 1 {
		t.Errorf("rename summary %+v", r)
	}
	if want := []string{"arch/x (project)"}; !reflect.DeepEqual(r.Collisions, want) {
		t.Errorf("collisions %v, want %v", r.Collisions, want)
	}
	for _, c := range sum.Checks {
		if !c.OK {
			t.Errorf("check failed after rename: %+v", c)
		}
	}
	if n := scalar[int](t, db, "SELECT count(*) FROM observations WHERE topic_key = 'arch/x'"); n != 2 {
		t.Errorf("colliding observations were overwritten: %d left", n)
	}
}

func TestProjectRenameIsIdempotentAndReported(t *testing.T) {
	f := renameFixture(t)
	target := filepath.Join(t.TempDir(), "neugram.db")
	runImport(t, f.path(), target, false)
	db := openTarget(t, target)
	first := dump(t, db)
	sum := runImport(t, f.path(), target, false)
	if second := dump(t, db); second != first {
		t.Error("second run changed the target content")
	}
	var b strings.Builder
	sum.Write(&b)
	for _, want := range []string{"gh-microservices-v2 → gh-microservices", "arch/x (project)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("summary misses %q:\n%s", want, b.String())
		}
	}
}
