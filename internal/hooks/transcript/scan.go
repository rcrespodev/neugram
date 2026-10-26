// Package transcript extracts what a Claude Code turn did from its .jsonl transcript.
// Format notes: docs/specs/hooks.md.
package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

type Mode int

const (
	// Turn reads backwards from the end up to the last human message (Stop).
	Turn Mode = iota
	// Entire reads the whole file (SubagentStop).
	Entire
)

type Options struct {
	Mode Mode
	// SaveTools overrides DefaultSaveTools; names are matched exactly.
	SaveTools []string
}

var DefaultSaveTools = []string{
	"mcp__neugram__mem_save",
	"mcp__neugram__mem_update",
	"mcp__neugram__handoff_save",
	"mcp__neugram__worktree_register",
}

// Mutation is one durable change. Unresolved marks a target that holds a shell
// expansion: Path is then the raw word, or empty when no path is known.
type Mutation struct {
	Tool       string
	Path       string
	Command    string
	Unresolved bool
	// Worktree marks the mutation that is a worktree creation.
	Worktree bool
}

// Worktree is a created worktree. Unresolved leaves Path empty when it held an expansion.
type Worktree struct {
	Tool       string
	Path       string
	Unresolved bool
}

type Call struct {
	Tool  string
	Input map[string]any
}

type Result struct {
	Mutations []Mutation
	Worktrees []Worktree
	Saves     []Call
}

func (r *Result) Mutated() bool { return len(r.Mutations) > 0 }

type entry struct {
	Type   string `json:"type"`
	IsMeta bool   `json:"isMeta"`
	Cwd    string `json:"cwd"`
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

var chunkSize = 1 << 20

func Scan(path string, opts Options) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []entry
	if opts.Mode == Entire {
		entries, err = readAll(f)
	} else {
		entries, err = readTurn(f)
	}
	if err != nil {
		return nil, err
	}
	return analyze(entries, opts), nil
}

func parseLine(line []byte) (entry, bool) {
	var e entry
	line = bytes.TrimSpace(line)
	if len(line) == 0 || json.Unmarshal(line, &e) != nil {
		return e, false
	}
	return e, e.Type == "user" || e.Type == "assistant"
}

func readAll(r io.Reader) ([]entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, line := range bytes.Split(data, []byte("\n")) {
		if e, ok := parseLine(line); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func readTurn(f *os.File) ([]entry, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var reversed []entry
	pos := st.Size()
	var carry []byte
	for pos > 0 {
		n := int64(chunkSize)
		if n > pos {
			n = pos
		}
		pos -= n
		chunk := make([]byte, n, n+int64(len(carry)))
		if _, err := f.ReadAt(chunk, pos); err != nil {
			return nil, err
		}
		chunk = append(chunk, carry...)
		lines := bytes.Split(chunk, []byte("\n"))
		carry = nil
		if pos > 0 {
			carry = lines[0]
			lines = lines[1:]
		}
		for i := len(lines) - 1; i >= 0; i-- {
			e, ok := parseLine(lines[i])
			if !ok {
				continue
			}
			if isBoundary(e) {
				return reverse(reversed), nil
			}
			reversed = append(reversed, e)
		}
	}
	return reverse(reversed), nil
}

func reverse(es []entry) []entry {
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	return es
}

var systemPrefixes = []string{"<local-command-", "<bash-stdout>", "<bash-stderr>"}

// Every automatic wake-up of the agent starts a new turn, like a human message.
var wakeKinds = map[string]bool{"task-notification": true, "peer": true}

var wakePrefixes = []string{
	"<task-notification>",
	"<teammate-message",
	"Another Claude session sent a message",
	"[Request interrupted by user",
}

func entryText(e entry) string {
	raw := bytes.TrimSpace(e.Message.Content)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return strings.TrimSpace(s)
	}
	var parts []string
	for _, b := range blocksOf(e) {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isBoundary(e entry) bool {
	if e.Type != "user" {
		return false
	}
	text := entryText(e)
	if text == "" {
		return false
	}
	if (e.Origin != nil && wakeKinds[e.Origin.Kind]) || hasAnyPrefix(text, wakePrefixes) {
		return true
	}
	return !e.IsMeta && !hasAnyPrefix(text, systemPrefixes)
}

func blocksOf(e entry) []block {
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return nil
	}
	return blocks
}

func analyze(entries []entry, opts Options) *Result {
	failed := map[string]bool{}
	answered := map[string]bool{}
	for _, e := range entries {
		if e.Type != "user" {
			continue
		}
		for _, b := range blocksOf(e) {
			if b.Type == "tool_result" {
				answered[b.ToolUseID] = true
				failed[b.ToolUseID] = b.IsError
			}
		}
	}

	saveTools := opts.SaveTools
	if saveTools == nil {
		saveTools = DefaultSaveTools
	}
	res := &Result{}
	for _, e := range entries {
		if e.Type != "assistant" {
			continue
		}
		for _, b := range blocksOf(e) {
			if b.Type != "tool_use" || !answered[b.ID] || failed[b.ID] {
				continue
			}
			classify(res, e.Cwd, b, saveTools)
		}
	}
	return res
}
