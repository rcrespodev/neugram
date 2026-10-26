package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
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
// git root basename, sole repo child, directory basename. A linked worktree resolves to its main repo.
// ctx bounds every git call; on expiry the git-based steps are skipped.
func Detect(ctx context.Context, dir string) (Result, error) {
	if dir == "" {
		dir = "."
	}
	root, mainRoot := gitRoots(ctx, dir)

	if res, ok, err := detectConfig(dir, root); ok || err != nil {
		return res, err
	}
	return detectByLadder(ctx, dir, mainRoot), nil
}

func detectByLadder(ctx context.Context, dir, mainRoot string) Result {
	if name := repoNameFromURL(gitOutput(ctx, dir, "remote", "get-url", "origin")); name != "" {
		return Result{normalize(name), GitRemote}
	}
	if mainRoot != "" {
		return Result{normalize(filepath.Base(mainRoot)), GitRoot}
	}
	if child := soleRepoChild(dir); child != "" {
		return Result{normalize(child), GitRoot}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return Result{normalize(filepath.Base(abs)), Basename}
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

func repoNameFromURL(remote string) string {
	remote = strings.TrimSpace(remote)
	repoPath := remote
	switch {
	case strings.Contains(remote, "://"):
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		repoPath = u.Path
	case scpLike(remote):
		repoPath = remote[strings.Index(remote, ":")+1:]
	}
	repoPath = strings.TrimSuffix(strings.TrimRight(repoPath, "/"), ".git")
	name := path.Base(repoPath)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// scpLike reports whether s is "[user@]host:path", the ssh shorthand that is not a URL.
func scpLike(s string) bool {
	colon := strings.Index(s, ":")
	return colon > 0 && !strings.Contains(s[:colon], "/")
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

var skippedChildren = map[string]bool{
	"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, "__pycache__": true,
}

// soleRepoChild returns the name of the only git repo among dir's direct children,
// or "" when there are none or several. Hidden and dependency directories are ignored.
func soleRepoChild(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var found string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || skippedChildren[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name, ".git")); err != nil {
			continue
		}
		if found != "" {
			return ""
		}
		found = name
	}
	return found
}

func notNameRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._- ", r)
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
	if name == "." || name == ".." || name == "" || strings.ContainsFunc(name, notNameRune) {
		return Result{}, true, fmt.Errorf("%w: %s: project_name must be a plain name", ErrInvalidConfig, path)
	}
	return Result{normalize(name), ConfigOverride}, true, nil
}
