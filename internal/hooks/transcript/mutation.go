package transcript

import (
	"encoding/json"
	"strings"
)

var writeTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

var scratchParts = []string{"/scratchpad/", "/tmp/", "/.git/", "/node_modules/"}

func isScratch(path string) bool {
	for _, p := range scratchParts {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

func classify(res *Result, cwd string, b block, saveTools []string) {
	var input map[string]any
	_ = json.Unmarshal(b.Input, &input)

	if writeTools[b.Name] {
		path, _ := input["file_path"].(string)
		if path == "" {
			path, _ = input["notebook_path"].(string)
		}
		if !isScratch(path) {
			res.Mutations = append(res.Mutations, Mutation{Tool: b.Name, Path: path})
		}
	}
}
