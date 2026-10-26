package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	GitRemote      = "git_remote"
	GitRoot        = "git_root"
	Basename       = "basename"
	ConfigOverride = "config_override"
)

const (
	unknownProject = "unknown"
	gitTimeout     = 2 * time.Second
)

var ErrInvalidConfig = errors.New("invalid .engram/config.json")

type Result struct {
	Project    string
	DetectedBy string
}

// Detect resolves the repo name for dir: config override, git remote origin,
// git root basename, directory basename. A linked worktree resolves to its main repo.
// ctx bounds every git call; on expiry the git-based steps are skipped.
func Detect(ctx context.Context, dir string) (Result, error) {
	if dir == "" {
		dir = "."
	}
	root, mainRoot := gitRoots(ctx, dir)

	if res, ok, err := detectConfig(dir, root); ok || err != nil {
		return res, err
	}
	if name := repoNameFromURL(gitOutput(ctx, dir, "remote", "get-url", "origin")); name != "" {
		return Result{normalize(name), GitRemote}, nil
	}
	if mainRoot != "" {
		return Result{normalize(filepath.Base(mainRoot)), GitRoot}, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return Result{normalize(filepath.Base(abs)), Basename}, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitRoots returns the toplevel of the checkout containing dir and the root of
// the repo that owns it (they differ in a linked worktree); both empty outside git.
func gitRoots(ctx context.Context, dir string) (root, mainRoot string) {
	out := gitOutput(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		return "", ""
	}
	root, common := lines[0], lines[1]
	if filepath.Base(common) == ".git" {
		return root, filepath.Dir(common)
	}
	return root, root
}

func repoNameFromURL(url string) string {
	url = strings.TrimSuffix(url, ".git")
	parts := strings.FieldsFunc(url, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func normalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "/" || n == "." {
		return unknownProject
	}
	return n
}

// detectConfig looks for .engram/config.json from dir up to the git root;
// outside git only dir itself counts.
func detectConfig(dir, root string) (Result, bool, error) {
	cur := realPath(dir)
	stop := cur
	if root != "" {
		stop = realPath(root)
	}
	for {
		res, ok, err := readConfig(cur)
		if ok || err != nil {
			return res, ok, err
		}
		parent := filepath.Dir(cur)
		if cur == stop || parent == cur {
			return Result{}, false, nil
		}
		cur = parent
	}
}

func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

func readConfig(dir string) (Result, bool, error) {
	path := filepath.Join(dir, ".engram", "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var cfg struct {
		ProjectName string `json:"project_name"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Result{}, true, fmt.Errorf("%w: %s: %v", ErrInvalidConfig, path, err)
	}
	name := strings.TrimSpace(cfg.ProjectName)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return Result{}, true, fmt.Errorf("%w: %s: project_name must be a plain name", ErrInvalidConfig, path)
	}
	return Result{normalize(name), ConfigOverride}, true, nil
}
