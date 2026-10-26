package project

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

type replayCounts struct {
	total, noDirectory, missingDirectory, matched                          int
	worktree, gitChild, configLater, noGitNow, manualSave, childrenChanged int
	unexplained                                                            []string
}

// TestReplayImportedSessions compares, session by session, the project that
// Engram recorded with what Detect gives today for the same directory.
func TestReplayImportedSessions(t *testing.T) {
	dbPath := os.Getenv("NEUGRAM_REPLAY_DB")
	if dbPath == "" {
		t.Skip("NEUGRAM_REPLAY_DB no definido")
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, directory, project FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var c replayCounts
	detected := map[string]Result{}
	for rows.Next() {
		var id, dir, recorded string
		if err := rows.Scan(&id, &dir, &recorded); err != nil {
			t.Fatal(err)
		}
		c.total++
		if dir == "" {
			c.noDirectory++
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			c.missingDirectory++
			continue
		}
		got, ok := detected[dir]
		if !ok {
			got, err = Detect(context.Background(), dir)
			if err != nil {
				got = Result{DetectedBy: err.Error()}
			}
			detected[dir] = got
		}
		switch {
		case got.Project == recorded:
			c.matched++
		case isLinkedWorktree(dir) && recorded == engramWorktreeName(dir):
			c.worktree++
		case got.DetectedBy == Basename && hasRepoChild(dir, recorded):
			c.gitChild++
		case got.DetectedBy == ConfigOverride && withoutConfig(dir) == recorded:
			c.configLater++
		case recorded == normalize(filepath.Base(dir)) && !isLinkedWorktree(dir):
			c.childrenChanged++
		case got.DetectedBy == Basename:
			c.noGitNow++
		case strings.HasPrefix(id, "manual-save-"):
			c.manualSave++
		default:
			c.unexplained = append(c.unexplained, fmt.Sprintf("%s %s: detectado %q (%s), registrado %q", id, dir, got.Project, got.DetectedBy, recorded))
		}
	}
	t.Logf("sesiones=%d sin_directory=%d directory_inexistente=%d coinciden=%d worktree_sin_remote=%d git_child=%d config_posterior=%d dir_sin_git_hoy=%d manual_save=%d basename_registrado=%d inexplicadas=%d",
		c.total, c.noDirectory, c.missingDirectory, c.matched, c.worktree, c.gitChild, c.configLater, c.noGitNow, c.manualSave, c.childrenChanged, len(c.unexplained))
	if len(c.unexplained) > 0 {
		t.Fatalf("discrepancias sin explicar:\n%s", strings.Join(c.unexplained, "\n"))
	}
}

func withoutConfig(dir string) string {
	_, mainRoot := gitRoots(context.Background(), dir)
	return detectByLadder(context.Background(), dir, mainRoot).Project
}

func isLinkedWorktree(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && !info.IsDir()
}

func engramWorktreeName(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return normalize(filepath.Base(strings.TrimSpace(string(out))))
}

func hasRepoChild(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && normalize(e.Name()) == name {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), ".git")); err == nil {
				return true
			}
		}
	}
	return false
}
