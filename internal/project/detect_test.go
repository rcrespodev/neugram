package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	initRepo(t, dir, remote)
	return dir
}

func initRepo(t *testing.T, dir, remote string) {
	t.Helper()
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	if remote != "" {
		git(t, dir, "remote", "add", "origin", remote)
	}
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
		{"cwd que es un archivo cae al basename", func(t *testing.T) string {
			f := filepath.Join(mkdir(t, filepath.Join(t.TempDir(), "dir")), "Archivo.txt")
			if err := os.WriteFile(f, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return f
		}, Result{"archivo.txt", Basename}},
		{"symlink a un repo resuelve el repo", func(t *testing.T) string {
			link := filepath.Join(t.TempDir(), "enlace")
			if err := os.Symlink(newRepo(t, "real", ""), link); err != nil {
				t.Fatal(err)
			}
			return link
		}, Result{"real", GitRoot}},
		{"raiz del filesystem es unknown", func(t *testing.T) string {
			return "/"
		}, Result{"unknown", Basename}},
		{"padre con un unico repo hijo da el hijo", func(t *testing.T) string {
			p := mkdir(t, filepath.Join(t.TempDir(), "Padre"))
			initRepo(t, mkdir(t, filepath.Join(p, "Hijo")), "")
			mkdir(t, filepath.Join(p, "docs"))
			return p
		}, Result{"hijo", GitRoot}},
		{"padre con dos repos hijos cae al basename", func(t *testing.T) string {
			p := mkdir(t, filepath.Join(t.TempDir(), "Padre"))
			initRepo(t, mkdir(t, filepath.Join(p, "uno")), "")
			initRepo(t, mkdir(t, filepath.Join(p, "dos")), "")
			return p
		}, Result{"padre", Basename}},
		{"repos ocultos o de ruido no cuentan como hijo", func(t *testing.T) string {
			p := mkdir(t, filepath.Join(t.TempDir(), "padre"))
			initRepo(t, mkdir(t, filepath.Join(p, ".oculto")), "")
			initRepo(t, mkdir(t, filepath.Join(p, "node_modules")), "")
			return p
		}, Result{"padre", Basename}},
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
			got, err := Detect(context.Background(), tc.setup(t))
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
		if _, err := Detect(context.Background(), d); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: got %v, want ErrInvalidConfig", body, err)
		}
	}
}
