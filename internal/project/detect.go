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
func Detect(dir string) (Result, error) {
	if dir == "" {
		dir = "."
	}
	if strings.HasPrefix(dir, "-") {
		dir = "./" + dir
	}
	root := gitOutput(dir, "rev-parse", "--show-toplevel")

	if res, ok, err := detectConfig(dir, root); ok || err != nil {
		return res, err
	}
	if name := repoNameFromURL(gitOutput(dir, "remote", "get-url", "origin")); name != "" {
		return Result{normalize(name), GitRemote}, nil
	}
	if root != "" {
		return Result{normalize(filepath.Base(mainRepoRoot(dir, root))), GitRoot}, nil
	}
	base := filepath.Base(realPath(dir))
	if base == "" || base == "." {
		base = unknownProject
	}
	return Result{normalize(base), Basename}, nil
}

func gitOutput(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// mainRepoRoot maps a linked worktree root to the root of the repo that owns it.
func mainRepoRoot(dir, root string) string {
	common := gitOutput(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return root
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
	if n == "" {
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
	if errors.Is(err, os.ErrNotExist) {
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
