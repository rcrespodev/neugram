package project

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newRepo(t *testing.T, name, remote string) string {
	t.Helper()
	dir := mkdir(t, filepath.Join(t.TempDir(), name))
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	if remote != "" {
		git(t, dir, "remote", "add", "origin", remote)
	}
	return dir
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	mkdir(t, filepath.Join(dir, ".engram"))
	if err := os.WriteFile(filepath.Join(dir, ".engram", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addWorktree(t *testing.T, repo string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "otro-nombre")
	git(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	return wt
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string
		want  Result
	}{
		{"remote ssh con .git", func(t *testing.T) string {
			return newRepo(t, "dir", "git@github.com:acme/Mi-Repo.git")
		}, Result{"mi-repo", GitRemote}},
		{"remote ssh sin .git", func(t *testing.T) string {
			return newRepo(t, "dir", "git@github.com:acme/repo")
		}, Result{"repo", GitRemote}},
		{"remote https con .git", func(t *testing.T) string {
			return newRepo(t, "dir", "https://github.com/acme/repo.git")
		}, Result{"repo", GitRemote}},
		{"remote https sin .git", func(t *testing.T) string {
			return newRepo(t, "dir", "https://github.com/acme/repo")
		}, Result{"repo", GitRemote}},
		{"sin remote usa la raiz git", func(t *testing.T) string {
			return newRepo(t, "MiRaiz", "")
		}, Result{"miraiz", GitRoot}},
		{"subdirectorio del repo", func(t *testing.T) string {
			d := newRepo(t, "dir", "git@github.com:acme/repo.git")
			return mkdir(t, filepath.Join(d, "a", "b"))
		}, Result{"repo", GitRemote}},
		{"subdirectorio sin remote", func(t *testing.T) string {
			d := newRepo(t, "raiz", "")
			return mkdir(t, filepath.Join(d, "a"))
		}, Result{"raiz", GitRoot}},
		{"worktree con remote da el repo", func(t *testing.T) string {
			return addWorktree(t, newRepo(t, "dir", "git@github.com:acme/repo.git"))
		}, Result{"repo", GitRemote}},
		{"worktree sin remote da la raiz del repo", func(t *testing.T) string {
			return addWorktree(t, newRepo(t, "principal", ""))
		}, Result{"principal", GitRoot}},
		{"config override en la raiz", func(t *testing.T) string {
			d := newRepo(t, "dir", "git@github.com:acme/repo.git")
			writeConfig(t, d, `{"project_name":"Forzado"}`)
			return d
		}, Result{"forzado", ConfigOverride}},
		{"config del subdirectorio gana al de la raiz", func(t *testing.T) string {
			d := newRepo(t, "dir", "")
			writeConfig(t, d, `{"project_name":"raiz"}`)
			sub := mkdir(t, filepath.Join(d, "pkg"))
			writeConfig(t, sub, `{"project_name":"sub"}`)
			return sub
		}, Result{"sub", ConfigOverride}},
		{"fuera de git usa basename", func(t *testing.T) string {
			return mkdir(t, filepath.Join(t.TempDir(), "Suelto"))
		}, Result{"suelto", Basename}},
		{"fuera de git con config", func(t *testing.T) string {
			d := mkdir(t, filepath.Join(t.TempDir(), "suelto"))
			writeConfig(t, d, `{"project_name":"cfg"}`)
			return d
		}, Result{"cfg", ConfigOverride}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Detect(tc.setup(t))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDetectInvalidConfig(t *testing.T) {
	for _, body := range []string{`{`, `{"project_name":""}`, `{"project_name":"a/b"}`} {
		d := newRepo(t, "dir", "")
		writeConfig(t, d, body)
		if _, err := Detect(d); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: got %v, want ErrInvalidConfig", body, err)
		}
	}
}

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
	rows, err := db.Query(`SELECT directory, GROUP_CONCAT(DISTINCT project), COUNT(*) FROM sessions WHERE directory <> '' GROUP BY directory`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var matched, missing, worktrees int
	var mismatches []string
	for rows.Next() {
		var dir, recorded string
		var n int
		if err := rows.Scan(&dir, &recorded, &n); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); err != nil {
			missing++
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && !info.IsDir() {
			worktrees++
			continue
		}
		got, err := Detect(dir)
		if err == nil && slices.Contains(strings.Split(recorded, ","), got.Project) {
			matched++
			continue
		}
		mismatches = append(mismatches, fmt.Sprintf("%s: detectado %q (%s, %v), registrado %q (%d sesiones)", dir, got.Project, got.DetectedBy, err, recorded, n))
	}
	for _, m := range mismatches {
		t.Log(m)
	}
	t.Logf("coinciden=%d worktrees_omitidos=%d inexistentes_omitidos=%d discrepancias=%d", matched, worktrees, missing, len(mismatches))
}
