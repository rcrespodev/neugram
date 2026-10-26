package mcp

import "encoding/json"

type prop = map[string]any

func str(desc string) prop  { return prop{"type": "string", "description": desc} }
func flag(desc string) prop { return prop{"type": "boolean", "description": desc} }
func num(desc string) prop  { return prop{"type": "number", "description": desc} }
func obj(desc string) prop  { return prop{"type": "object", "description": desc} }
func integer(desc string, max int) prop {
	return prop{"type": "integer", "minimum": 0, "maximum": max, "description": desc}
}
func enum(desc string, values ...string) prop {
	return prop{"type": "string", "enum": values, "description": desc}
}
func oneOrMany(desc string) prop {
	return prop{"anyOf": []prop{{"type": "string"}, {"type": "array", "items": prop{"type": "string"}}}, "description": desc}
}
func nullable(desc string, values ...any) prop {
	p := prop{"type": []string{"string", "null"}, "description": desc}
	if len(values) > 0 {
		p["enum"] = values
	}
	return p
}

var (
	obsTypes = []string{"bugfix", "decision", "architecture", "discovery", "pattern", "config", "preference", "handoff", "session_summary"}
	statuses = []string{"todo", "in-progress", "review", "blocked", "done"}
	scopes   = []string{"project", "personal"}
)

const sessionIDDesc = "Session id printed by the session-start hook (CLAUDE_CODE_SESSION_ID). Never read from the environment."

// toolSpec is one MCP tool. Writers require session_id; readers accept it.
type toolSpec struct {
	name     string
	desc     string
	writes   bool
	props    map[string]prop
	required []string
}

func (t toolSpec) inputSchema() json.RawMessage {
	props := map[string]prop{}
	for k, v := range t.props {
		props[k] = v
	}
	props["session_id"] = str(sessionIDDesc)
	required := append([]string{}, t.required...)
	if t.writes {
		required = append(required, "session_id")
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required})
	return raw
}

var tools = []toolSpec{
	{
		name:   "mem_save",
		desc:   "Save an observation tied to a task. The daemon adds or fixes the **Task** footer.",
		writes: true,
		props: map[string]prop{
			"title":          str("Verb + what, short and searchable"),
			"content":        str("What / Why / Where / Learned, ending with the **Task** line"),
			"type":           enum("Observation type", obsTypes...),
			"task_id":        str("Task key; defaults to the session pin; 'none' saves without a task"),
			"topic_key":      str("Upsert key per (topic_key, project, scope)"),
			"scope":          enum("Scope", scopes...),
			"project":        str("Repo override; defaults to the session project"),
			"capture_prompt": flag("Accepted and ignored"),
		},
		required: []string{"title", "content", "type"},
	},
	{
		name:   "mem_update",
		desc:   "Modify a saved observation by sync_id.",
		writes: true,
		props: map[string]prop{
			"sync_id":   str("Public key of the observation"),
			"title":     str("New title"),
			"content":   str("New content"),
			"type":      enum("Observation type", obsTypes...),
			"topic_key": nullable("null clears it"),
			"task_id":   str("Moves the observation to this task and rewrites its footer"),
			"scope":     enum("Scope", scopes...),
			"kind":      nullable("handoff or null", "handoff", nil),
		},
		required: []string{"sync_id"},
	},
	{
		name: "mem_search",
		desc: "Full-text search over observations, crossed with task, repo, type and column filters.",
		props: map[string]prop{
			"query":             str("Literal terms, no FTS5 operators"),
			"match_mode":        enum("all (default) or any", "all", "any"),
			"task_id":           str("Restrict to this task; the pin is not used"),
			"include_ancestors": flag("With task_id: add ancestors (default true)"),
			"include_subtasks":  flag("With task_id: add the whole subtree (default false)"),
			"project":           str("Repo"),
			"product":           str("Product name"),
			"type":              oneOrMany("One or several types"),
			"kind":              nullable("handoff to search only handoffs", "handoff", nil),
			"scope":             enum("Scope", scopes...),
			"since":             str("ISO 8601 lower bound on created_at"),
			"until":             str("ISO 8601 upper bound on created_at"),
			"where":             obj("Fixed or custom observation columns"),
			"full":              flag("Return whole content"),
			"limit":             integer("Default 20", 200),
			"offset":            integer("Default 0", 1<<31-1),
		},
		required: []string{"query"},
	},
	{
		name:   "mem_judge",
		desc:   "Resolve one conflict candidate returned by mem_save.",
		writes: true,
		props: map[string]prop{
			"judgment_id": str("The rel-... id of that candidate"),
			"relation":    enum("Relation", "supersedes", "conflicts_with", "related", "compatible", "scoped", "not_conflict"),
			"reason":      str("Why"),
			"evidence":    str("What backs it"),
			"confidence":  num("0 to 1"),
		},
		required: []string{"judgment_id", "relation"},
	},
	{
		name:   "task_pin",
		desc:   "Pin the session to a task, creating it when title is given. 'none' is a valid pin.",
		writes: true,
		props: map[string]prop{
			"task_id": str("Task key"),
			"title":   str("Creates the task if the key does not exist"),
			"product": str("Product name; only when creating"),
			"parent":  str("Parent key (level 1 or 2); only when creating"),
			"tracker": str("clickup:<id>:<url>"),
			"notes":   str("Notes"),
		},
		required: []string{"task_id"},
	},
	{
		name: "task_list",
		desc: "Tasks with product, status, activity and live worktrees.",
		props: map[string]prop{
			"status":       oneOrMany("One or several statuses"),
			"product":      str("Product name"),
			"parent":       str("Direct children of this task"),
			"roots_only":   flag("Only level-1 tasks"),
			"include_done": flag("Include done tasks"),
			"tree":         flag("Nest children under parents"),
			"where":        obj("Fixed or custom task columns"),
			"limit":        integer("Default 50", 200),
		},
	},
	{
		name:   "task_status",
		desc:   "Change a task's status. The user declares it; hooks never call this.",
		writes: true,
		props: map[string]prop{
			"task_id": str("Task key; defaults to the session pin"),
			"status":  enum("Status", statuses...),
		},
		required: []string{"status"},
	},
	{
		name: "task_show",
		desc: "Full task card: identity, hierarchy, sessions, worktrees, handoffs and progress.",
		props: map[string]prop{
			"task_id":            str("Task key; defaults to the session pin"),
			"include_subtasks":   flag("Default true"),
			"observations_limit": integer("Default 10", 100),
		},
	},
	{
		name:   "worktree_register",
		desc:   "Register a freshly created git worktree for a task.",
		writes: true,
		props: map[string]prop{
			"path":    str("Absolute path; the upsert key"),
			"repo":    str("Repo"),
			"branch":  str("Branch"),
			"base":    str("Branch it came from"),
			"purpose": str("One line"),
			"task_id": str("Task key; defaults to the session pin"),
		},
		required: []string{"path", "repo", "branch"},
	},
	{
		name:   "handoff_save",
		desc:   "Save the session handoff; one per session, repeating updates it.",
		writes: true,
		props: map[string]prop{
			"content": str("Markdown; the daemon adds the **Task** footer"),
			"task_id": str("Task key; defaults to the session pin"),
			"title":   str("Defaults to the first line of the State"),
		},
		required: []string{"content"},
	},
}
