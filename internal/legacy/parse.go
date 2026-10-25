package legacy

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	controlKey = regexp.MustCompile(`^task/([^/]+)/(identity|status|session/([^/]+)|wt/([^/]+)|handoff/([^/]+)|progress/([^/]+)/([^/]+))$`)
	keyToken   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	parentHint = regexp.MustCompile(`(?i)(?:^|[\s(·,])(?:parent|padre|subtarea de|epic|épica)\s+([A-Za-z0-9][A-Za-z0-9._-]*)`)
	fieldLine  = regexp.MustCompile(`^\s*(?:-\s*)?(?:\*\*)?([A-Za-z][A-Za-z ]*?)(?:\*\*)?:\s*(.*)$`)
	jsonFence  = regexp.MustCompile("(?s)```json\\s*(.*?)```")
	// jsonFenceToLast covers fences whose JSON strings embed ``` themselves.
	jsonFenceToLast = regexp.MustCompile("(?s)```json\\s*(.*)```")
)

var statuses = map[string]bool{"todo": true, "in-progress": true, "review": true, "blocked": true, "done": true}

// noTaskKeys are the footer values that mean "sin tarea" (docs/specs/import-legacy.md, Fase 3).
var noTaskKeys = map[string]bool{"ninguna": true, "none": true}

type recordKind string

const (
	kindIdentity recordKind = "identity"
	kindStatus   recordKind = "status"
	kindSession  recordKind = "session"
	kindWorktree recordKind = "wt"
	kindHandoff  recordKind = "handoff"
	kindProgress recordKind = "progress"
)

type controlRef struct {
	kind    recordKind
	taskRef string // the <id> segment of the topic_key
	session string
	slot    string
}

func parseControlKey(topicKey string) (controlRef, bool) {
	m := controlKey.FindStringSubmatch(topicKey)
	if m == nil {
		return controlRef{}, false
	}
	ref := controlRef{taskRef: m[1]}
	switch {
	case m[2] == "identity":
		ref.kind = kindIdentity
	case m[2] == "status":
		ref.kind = kindStatus
	case m[3] != "":
		ref.kind, ref.session = kindSession, m[3]
	case m[4] != "":
		ref.kind = kindWorktree
	case m[5] != "":
		ref.kind, ref.session = kindHandoff, m[5]
	default:
		ref.kind, ref.session, ref.slot = kindProgress, m[6], m[7]
	}
	return ref, true
}

// footer is a parsed `**Task**:` line. key is empty when the line names no task
// (none is true) or when the value is not a single key token.
type footer struct {
	key   string
	title string
	none  bool
}

// parseFooter reads both footer shapes of docs/specs/import-legacy.md (Fase 3).
func parseFooter(line string) (footer, bool) {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, "**Task**:")
	if !ok {
		return footer{}, false
	}
	head, title, _ := strings.Cut(strings.TrimSpace(rest), " — ")
	if i := strings.LastIndex(head, " · "); i >= 0 {
		head = head[i+len(" · "):]
	}
	head = strings.TrimSpace(head)
	f := footer{title: strings.TrimSpace(title)}
	switch {
	case noTaskKeys[strings.ToLower(head)]:
		f.none = true
	case keyToken.MatchString(head):
		f.key = head
	}
	return f, true
}

func lastFooter(content string) (footer, bool) {
	lines := strings.Split(strings.TrimRight(content, " \t\r\n"), "\n")
	return parseFooter(lines[len(lines)-1])
}

type identity struct {
	key, title      string
	product         string
	trackerKind     string
	trackerID       string
	trackerURL      string
	notes           string
	parentHintedKey string
	parentRuleKey   string
}

func parseIdentity(content string) identity {
	var id identity
	var rest []string
	for _, line := range strings.Split(content, "\n") {
		if f, ok := parseFooter(line); ok {
			if id.key == "" && f.key != "" {
				id.key, id.title = f.key, f.title
			}
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "**Product**:"); ok {
			id.product = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "**Tracker**:"); ok {
			id.trackerKind, id.trackerID, id.trackerURL = parseTracker(v)
			continue
		}
		rest = append(rest, line)
	}
	body := strings.TrimSpace(strings.Join(rest, "\n"))
	var notes []string
	if id.product != "" {
		notes = append(notes, provenanceNotePrefix+id.product)
		if m := parentHint.FindStringSubmatch(id.product); m != nil {
			id.parentHintedKey = m[1]
		}
		id.parentRuleKey = productParentKey(id.product)
	}
	if body != "" {
		notes = append(notes, body)
	}
	id.notes = strings.Join(notes, "\n\n")
	return id
}

func parseTracker(v string) (kind, id, url string) {
	parts := strings.Split(strings.TrimSpace(v), " · ")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	kind = parts[0]
	if len(parts) > 1 {
		id = strings.TrimSpace(strings.TrimPrefix(parts[1], "id "))
	}
	if len(parts) > 2 {
		url = parts[2]
	}
	return kind, id, url
}

const (
	workProduct = "tripulse"
	// provenanceNotePrefix marks the original **Product** text in tasks.notes
	// as import provenance, not as a note the user wrote.
	provenanceNotePrefix = "[import-legacy] **Product** original: "
)

// personalProducts are the only products kept apart; every other **Product**
// string belongs to workProduct (docs/specs/import-legacy.md, Productos).
var personalProducts = []string{"ai-review", "dev-loop", "engram-fork"}

// productParentRules assign a parent to tasks whose **Product** text names a
// workstream, for the tasks that carry no explicit parent hint.
var productParentRules = []struct{ productContains, parentKey string }{
	{"socios de negocio", "TRIPULSE-3945"},
}

func productParentKey(product string) string {
	lower := strings.ToLower(product)
	for _, r := range productParentRules {
		if strings.Contains(lower, r.productContains) {
			return r.parentKey
		}
	}
	return ""
}

// canonicalProduct maps a free-text **Product** to its product name.
func canonicalProduct(s string) string {
	cut := len(s)
	for _, sep := range []string{" · ", " — ", " / ", " ("} {
		if i := strings.Index(s, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	head := strings.TrimSpace(s[:cut])
	for _, p := range personalProducts {
		if strings.EqualFold(head, p) {
			return p
		}
	}
	return workProduct
}

func parseStatus(content string) (string, bool) {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return "todo", false
	}
	w := strings.ToLower(strings.Trim(fields[0], "*`.:"))
	if statuses[w] {
		return w, true
	}
	return "todo", false
}

type worktree struct {
	Path             string `json:"path"`
	Repo             string `json:"repo"`
	Branch           string `json:"branch"`
	Base             string `json:"base"`
	CreatedBySession string `json:"created_by_session"`
	Purpose          string `json:"purpose"`
}

func parseWorktree(content string) (worktree, bool) {
	var wt worktree
	if unmarshalFence(content, &wt) && wt.Path != "" {
		return wt, true
	}
	wt = worktree{}
	for _, line := range strings.Split(content, "\n") {
		m := fieldLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		switch strings.ToLower(strings.TrimSpace(m[1])) {
		case "path":
			wt.Path = v
		case "repo":
			wt.Repo = v
		case "branch":
			wt.Branch = v
		case "base":
			wt.Base = v
		case "created by session":
			wt.CreatedBySession = v
		case "purpose":
			wt.Purpose = v
		}
	}
	return wt, strings.HasPrefix(wt.Path, "/")
}

type progress struct {
	Status     string          `json:"status"`
	References json.RawMessage `json:"references"`
	Summary    stringList      `json:"summary"`
	NextSteps  stringList      `json:"next_steps"`
}

// stringList also accepts a bare string and non-string items, which some
// legacy reports wrote; an item that is not a string keeps its JSON text.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*l = []string{s}
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return err
	}
	*l = make(stringList, len(items))
	for i, item := range items {
		if err := json.Unmarshal(item, &(*l)[i]); err != nil {
			(*l)[i] = string(item)
		}
	}
	return nil
}

var roles = map[string]bool{"explore": true, "impl": true, "review": true}

func parseProgress(slot, content string) (role string, p progress, ok bool) {
	role, _, _ = strings.Cut(slot, "-")
	if !roles[role] || !unmarshalFence(content, &p) {
		return "", p, false
	}
	if p.Status != "done" && p.Status != "blocked" {
		return "", p, false
	}
	return role, p, true
}

func unmarshalFence(content string, v any) bool {
	for _, re := range []*regexp.Regexp{jsonFence, jsonFenceToLast} {
		if m := re.FindStringSubmatch(content); m != nil && json.Unmarshal([]byte(m[1]), v) == nil {
			return true
		}
	}
	return false
}
