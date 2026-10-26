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

var nullSinks = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true}

// Operators that end one command and start the next.
var commandBreaks = map[string]bool{"&&": true, "||": true, "|": true, ";": true, "&": true, "(": true, ")": true}

// Operators whose next word is a file that gets written.
var writeRedirects = map[string]bool{">": true, ">>": true, ">|": true, "&>": true, "&>>": true}

// Words that precede the real command without changing what it mutates.
var shellKeywords = map[string]bool{
	"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true,
	"until": true, "!": true, "{": true, "time": true, "command": true, "exec": true,
	"nohup": true, "builtin": true,
}

// Wrappers that take their own flags, and which of those flags take a value.
var wrappers = map[string]map[string]bool{
	"sudo":  {"-u": true, "-g": true},
	"env":   {"-u": true},
	"xargs": {"-I": true, "-n": true, "-P": true, "-L": true, "-d": true, "-E": true, "-s": true},
	"nice":  {"-n": true},
}

var assignment = regexp.MustCompile(`^[A-Za-z_]\w*=`)

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
		bash := analyzeBash(command, cwd)
		res.Mutations = append(res.Mutations, bash.mutations...)
		res.Worktrees = append(res.Worktrees, bash.worktrees...)
	case b.Name == "EnterWorktree":
		p := enterWorktreePath(input, cwd)
		res.Worktrees = append(res.Worktrees, Worktree{Tool: b.Name, Path: p})
		res.Mutations = append(res.Mutations, Mutation{Tool: b.Name, Path: p, Worktree: true})
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

func resolvePath(p, base string) string {
	if p == "" || path.IsAbs(p) || strings.HasPrefix(p, "~") || base == "" {
		return p
	}
	return path.Join(base, p)
}

type bashResult struct {
	mutations []Mutation
	worktrees []Worktree
}

type analyzer struct {
	command string
	dir     string
	seen    map[string]bool
	out     bashResult
}

func analyzeBash(command, cwd string) bashResult {
	a := &analyzer{command: stripHeredocBodies(command), dir: cwd, seen: map[string]bool{}}
	tokens := shellTokens(a.command)
	var seg []tok
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case t.op && commandBreaks[t.text]:
			a.segment(seg)
			seg = nil
		case t.op && writeRedirects[t.text]:
			if i+1 < len(tokens) && !tokens[i+1].op {
				i++
				a.target(tokens[i])
			}
		case t.op:
			if i+1 < len(tokens) && !tokens[i+1].op {
				i++
			}
		default:
			seg = append(seg, t)
		}
	}
	a.segment(seg)
	return a.out
}

func (a *analyzer) bare() {
	if !a.seen[""] {
		a.seen[""] = true
		a.out.mutations = append(a.out.mutations, Mutation{Tool: "Bash", Command: a.command})
	}
}

// target records a file the command writes. A word we cannot expand is a non-scratch mutation.
func (a *analyzer) target(t tok) {
	raw := t.text
	if raw == "" || nullSinks[raw] || isScratch(raw) {
		return
	}
	p, unresolved := raw, t.dyn
	if !t.dyn {
		p = resolvePath(raw, a.dir)
		if isScratch(p) {
			return
		}
	}
	if a.seen[p] {
		return
	}
	a.seen[p] = true
	a.out.mutations = append(a.out.mutations, Mutation{Tool: "Bash", Path: p, Command: a.command, Unresolved: unresolved})
}

func (a *analyzer) segment(seg []tok) {
	viaXargs := false
	for len(seg) > 0 {
		head := seg[0].text
		switch {
		case shellKeywords[head] || assignment.MatchString(head):
			seg = seg[1:]
		case wrappers[head] != nil:
			viaXargs = viaXargs || head == "xargs"
			seg = skipFlags(seg[1:], wrappers[head])
		default:
			a.command1(seg[0].text, seg[1:], viaXargs)
			return
		}
	}
}

func skipFlags(seg []tok, valueFlags map[string]bool) []tok {
	for len(seg) > 0 && strings.HasPrefix(seg[0].text, "-") && len(seg[0].text) > 1 {
		if valueFlags[seg[0].text] && len(seg) > 1 {
			seg = seg[1:]
		}
		seg = seg[1:]
	}
	return seg
}

func (a *analyzer) command1(name string, args []tok, viaXargs bool) {
	switch name {
	case "cd":
		if len(args) == 0 || args[0].dyn || args[0].text == "-" {
			a.dir = ""
		} else {
			a.dir = resolvePath(args[0].text, a.dir)
		}
	case "git":
		a.git(args)
	case "gh":
		a.gh(args)
	case "tee":
		for _, t := range positional(args, nil) {
			a.target(t)
		}
	case "sed", "perl":
		a.inPlaceEditor(name, args)
	default:
		a.fileCommand(name, args, viaXargs)
	}
}

var gitValueFlags = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}

func (a *analyzer) git(args []tok) {
	base := a.dir
	for len(args) > 0 && strings.HasPrefix(args[0].text, "-") {
		flag := args[0].text
		if gitValueFlags[flag] && len(args) > 1 {
			if flag == "-C" && !args[1].dyn {
				base = resolvePath(args[1].text, base)
			}
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	switch args[0].text {
	case "commit", "push":
		a.bare()
	case "worktree":
		if len(args) > 1 && args[1].text == "add" {
			a.worktreeAdd(args[2:], base)
		}
	}
}

var worktreeValueFlags = map[string]bool{"-b": true, "-B": true, "--reason": true, "--lock-reason": true}

func (a *analyzer) worktreeAdd(args []tok, base string) {
	pos := positional(args, worktreeValueFlags)
	if len(pos) == 0 {
		return
	}
	wt := Worktree{Tool: "Bash"}
	if pos[0].dyn {
		wt.Unresolved = true
	} else {
		wt.Path = resolvePath(pos[0].text, base)
	}
	a.out.worktrees = append(a.out.worktrees, wt)
	a.out.mutations = append(a.out.mutations, Mutation{Tool: "Bash", Path: wt.Path, Command: a.command, Unresolved: wt.Unresolved, Worktree: true})
}

func (a *analyzer) gh(args []tok) {
	pos := positional(args, map[string]bool{"-R": true, "--repo": true})
	if len(pos) >= 2 && pos[1].text == "create" && (pos[0].text == "pr" || pos[0].text == "issue") {
		a.bare()
	}
}

// positional returns the non-flag words, skipping the values of valueFlags.
func positional(args []tok, valueFlags map[string]bool) []tok {
	var out []tok
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "--":
			out = append(out, args[i+1:]...)
			return out
		case valueFlags[text]:
			i++
		case strings.HasPrefix(text, "-") && len(text) > 1:
		default:
			out = append(out, args[i])
		}
	}
	return out
}

var (
	sedInPlace  = regexp.MustCompile(`^(?:-[nEsz]*i|--in-place)`)
	perlInPlace = regexp.MustCompile(`^-[pnal0-9]*i`)
)

func (a *analyzer) inPlaceEditor(name string, args []tok) {
	re := sedInPlace
	if name == "perl" {
		re = perlInPlace
	}
	inPlace, scriptFlag := false, false
	for _, t := range args {
		if re.MatchString(t.text) {
			inPlace = true
		}
		if t.text == "-e" || t.text == "-f" || strings.HasPrefix(t.text, "--expression") || strings.HasPrefix(t.text, "--file") {
			scriptFlag = true
		}
	}
	if !inPlace {
		return
	}
	files := positional(args, map[string]bool{"-e": true, "-f": true, "--expression": true, "--file": true})
	if !scriptFlag && len(files) > 0 {
		files = files[1:]
	}
	if len(files) == 0 {
		a.bare()
	}
	for _, f := range files {
		a.target(f)
	}
}

// Commands whose positional arguments are all paths they change.
var pathCommands = map[string]map[string]bool{
	"mv":    {"-t": true, "--target-directory": true, "-S": true, "--suffix": true},
	"rm":    {},
	"mkdir": {"-m": true, "--mode": true},
	"touch": {"-d": true, "-r": true, "-t": true, "--date": true, "--reference": true},
}

// Subcommands that change the dependency files of the project.
var packageCommands = map[string][]string{
	"npm": {"i", "install"},
	"go":  {"mod"},
}

func (a *analyzer) fileCommand(name string, args []tok, viaXargs bool) {
	if flags, ok := pathCommands[name]; ok {
		pos := positional(args, flags)
		if len(pos) == 0 && viaXargs {
			a.out.mutations = append(a.out.mutations, Mutation{Tool: "Bash", Command: a.command, Unresolved: true})
		}
		for _, t := range pos {
			a.target(t)
		}
		return
	}
	if subs, ok := packageCommands[name]; ok {
		if pos := positional(args, nil); len(pos) > 0 && slices.Contains(subs, pos[0].text) {
			a.bare()
		}
	}
}
