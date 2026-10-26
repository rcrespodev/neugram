package transcript

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
)

var writeTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

var scratchParts = []string{"/scratchpad/", "/tmp/", "/.git/", "/node_modules/"}

// Durable whatever paths they touch.
var publishCommands = []string{"git commit", "git push", "gh pr create", "gh issue create"}

var inPlaceEditors = []string{"sed -i", "tee "}

var nullSinks = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true}

// Commands whose non-flag arguments are the paths they mutate.
var pathMutators = map[string]bool{"mv": true, "rm": true, "mkdir": true, "touch": true}

// A token that names a file rather than a sed script (`s/a/b/`) or a flag.
var pathish = regexp.MustCompile(`^(?:/|\./|\.\./|~/)|^[\w.-]+/[\w./-]*\.\w+$`)

var heredocStart = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

func isScratch(p string) bool {
	for _, part := range scratchParts {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}

func classify(res *Result, cwd string, b block, saveTools []string) {
	var input map[string]any
	_ = json.Unmarshal(b.Input, &input)

	switch {
	case writeTools[b.Name]:
		p, _ := input["file_path"].(string)
		if p == "" {
			p, _ = input["notebook_path"].(string)
		}
		if !isScratch(p) {
			res.Mutations = append(res.Mutations, Mutation{Tool: b.Name, Path: p})
		}
	case b.Name == "Bash":
		command, _ := input["command"].(string)
		res.Mutations = append(res.Mutations, bashMutations(command, cwd)...)
		for _, p := range worktreeAdds(stripHeredocBodies(command), cwd) {
			res.Worktrees = append(res.Worktrees, Worktree{Tool: "Bash", Path: p})
			res.Mutations = append(res.Mutations, Mutation{Tool: "Bash", Path: p, Command: command})
		}
	case b.Name == "EnterWorktree":
		p := enterWorktreePath(input, cwd)
		res.Worktrees = append(res.Worktrees, Worktree{Tool: b.Name, Path: p})
		res.Mutations = append(res.Mutations, Mutation{Tool: b.Name, Path: p})
	case slices.Contains(saveTools, b.Name):
		res.Saves = append(res.Saves, Call{Tool: b.Name, Input: input})
	}
}

func enterWorktreePath(input map[string]any, cwd string) string {
	if p, _ := input["path"].(string); p != "" {
		return resolvePath(p, cwd)
	}
	if name, _ := input["name"].(string); name != "" && cwd != "" {
		return path.Join(cwd, ".claude", "worktrees", name)
	}
	return ""
}

var worktreeValueFlags = map[string]bool{"-b": true, "-B": true, "--reason": true, "--lock-reason": true}

// worktreeAdds returns the absolute path of each `git worktree add` in the command.
func worktreeAdds(command, cwd string) []string {
	var out []string
	tokens := shellTokens(command)
	for i := 0; i < len(tokens); {
		end := i
		for end < len(tokens) && !commandBreaks[tokens[end]] {
			end++
		}
		seg := tokens[i:end]
		i = end + 1
		if len(seg) == 0 || seg[0] != "git" {
			continue
		}
		base := cwd
		j := 1
		for j < len(seg) && strings.HasPrefix(seg[j], "-") {
			if (seg[j] == "-C" || seg[j] == "-c") && j+1 < len(seg) {
				if seg[j] == "-C" {
					base = resolvePath(seg[j+1], base)
				}
				j++
			}
			j++
		}
		if j+1 >= len(seg) || seg[j] != "worktree" || seg[j+1] != "add" {
			continue
		}
		for k := j + 2; k < len(seg); k++ {
			if worktreeValueFlags[seg[k]] {
				k++
			} else if !strings.HasPrefix(seg[k], "-") {
				out = append(out, resolvePath(seg[k], base))
				break
			}
		}
	}
	return out
}

func resolvePath(p, cwd string) string {
	if p == "" || path.IsAbs(p) || strings.HasPrefix(p, "~") || cwd == "" {
		return p
	}
	return path.Join(cwd, p)
}

func scratchOrOutside(raw, resolved string) bool {
	return isScratch(raw) || isScratch(resolved)
}

func bashMutations(command, cwd string) []Mutation {
	command = stripHeredocBodies(command)
	for _, marker := range publishCommands {
		if strings.Contains(command, marker) {
			return []Mutation{{Tool: "Bash", Command: command}}
		}
	}

	tokens := shellTokens(command)
	inPlace := false
	for _, marker := range inPlaceEditors {
		if strings.Contains(command, marker) {
			inPlace = true
		}
	}

	var targets []string
	for _, t := range redirectTargets(command) {
		if t = cleanTarget(t); t != "" && !nullSinks[t] {
			targets = append(targets, t)
		}
	}
	if inPlace {
		for _, t := range tokens {
			if pathish.MatchString(t) {
				targets = append(targets, t)
			}
		}
	}
	mutatorPaths, packageManager := scanSegments(tokens)
	targets = append(targets, mutatorPaths...)

	var out []Mutation
	seen := map[string]bool{}
	for _, t := range targets {
		resolved := resolvePath(t, cwd)
		if scratchOrOutside(t, resolved) || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, Mutation{Tool: "Bash", Path: resolved, Command: command})
	}
	if len(out) == 0 && (packageManager || (inPlace && len(targets) == 0)) {
		out = append(out, Mutation{Tool: "Bash", Command: command})
	}
	return out
}

func stripHeredocBodies(command string) string {
	if !heredocStart.MatchString(command) {
		return command
	}
	var kept []string
	delimiter := ""
	for _, line := range strings.Split(command, "\n") {
		if delimiter == "" {
			kept = append(kept, line)
			if m := heredocStart.FindStringSubmatch(line); m != nil {
				delimiter = m[1]
			}
		} else if strings.TrimSpace(line) == delimiter {
			delimiter = ""
		}
	}
	return strings.Join(kept, "\n")
}

// redirectTargets finds `>f` and `>>f`, excluding `2>`, `&>`, `>&2` and process substitution.
func redirectTargets(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '>' || (i > 0 && (s[i-1] == '&' || (s[i-1] >= '0' && s[i-1] <= '9'))) {
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '>' {
			j++
		}
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] == '&' || s[j] == '(' {
			continue
		}
		start := j
		for j < len(s) && !strings.ContainsRune(" \t\r\n;|&<>()", rune(s[j])) {
			j++
		}
		if j > start {
			out = append(out, s[start:j])
		}
		i = j - 1
	}
	return out
}

func cleanTarget(t string) string {
	t = strings.Trim(strings.TrimSpace(t), `'"`)
	if t == "" || strings.ContainsAny(t, "\"'{}\\") {
		return ""
	}
	return t
}

var commandBreaks = map[string]bool{"&&": true, "||": true, "|": true, ";": true, "&": true}

var leadingWrappers = map[string]bool{"sudo": true, "command": true, "env": true}

func scanSegments(tokens []string) (paths []string, packageManager bool) {
	for i := 0; i < len(tokens); {
		end := i
		for end < len(tokens) && !commandBreaks[tokens[end]] {
			end++
		}
		seg := tokens[i:end]
		i = end + 1
		for len(seg) > 0 && (leadingWrappers[seg[0]] || strings.Contains(seg[0], "=") && !strings.HasPrefix(seg[0], "-")) {
			seg = seg[1:]
		}
		if len(seg) == 0 {
			continue
		}
		switch {
		case pathMutators[seg[0]]:
			for _, a := range seg[1:] {
				if !strings.HasPrefix(a, "-") {
					paths = append(paths, a)
				}
			}
		case seg[0] == "go" && len(seg) > 1 && seg[1] == "mod":
			packageManager = true
		case seg[0] == "npm" && len(seg) > 1 && (seg[1] == "i" || seg[1] == "install"):
			packageManager = true
		}
	}
	return paths, packageManager
}

// shellTokens splits a command on whitespace honoring quotes, and emits
// unquoted control operators as their own tokens.
func shellTokens(s string) []string {
	var tokens []string
	var cur strings.Builder
	has := false
	flush := func() {
		if has {
			tokens = append(tokens, cur.String())
			cur.Reset()
			has = false
		}
	}
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, has = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			has = true
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '\n' || c == ';':
			flush()
			tokens = append(tokens, ";")
		case c == '&' || c == '|':
			flush()
			op := string(c)
			if i+1 < len(s) && s[i+1] == c {
				op += op
				i++
			}
			tokens = append(tokens, op)
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	flush()
	return tokens
}
