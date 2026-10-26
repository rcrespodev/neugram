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

type Mutation struct {
	Tool    string
	Path    string
	Command string
}

type Worktree struct {
	Tool string
	Path string
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
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

const chunkSize = 1 << 20

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
			if isHuman(e) {
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

var systemPrefixes = []string{"<local-command-", "<bash-stdout>", "<bash-stderr>", "<task-notification>"}

func isHuman(e entry) bool {
	if e.Type != "user" || e.IsMeta {
		return false
	}
	if e.Origin != nil && e.Origin.Kind != "" && e.Origin.Kind != "human" {
		return false
	}
	raw := bytes.TrimSpace(e.Message.Content)
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		for _, p := range systemPrefixes {
			if strings.HasPrefix(s, p) {
				return false
			}
		}
		return true
	}
	for _, b := range blocksOf(e) {
		if b.Type == "text" {
			return true
		}
	}
	return false
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
