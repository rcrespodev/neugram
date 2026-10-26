package transcript

import (
	"path/filepath"
	"reflect"
	"testing"
)

func worktreePaths(res *Result) []string {
	var out []string
	for _, w := range res.Worktrees {
		out = append(out, w.Path)
	}
	return out
}

func TestWorktrees(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
		want  []string
	}{
		{"path relativo se resuelve con el cwd", []step{bash("git worktree add ../wt-feature-x -b feature-x")}, []string{"/home/r/wt-feature-x"}},
		{"path despues del -b", []step{bash("git worktree add -b feature-x ../wt-feature-x")}, []string{"/home/r/wt-feature-x"}},
		{"path absoluto queda igual", []step{bash("git worktree add /home/r/wts/a")}, []string{"/home/r/wts/a"}},
		{"dos en un comando", []step{bash("git worktree add ../a && git worktree add ../b")}, []string{"/home/r/a", "/home/r/b"}},
		{"cwd de la entrada donde aparece", []step{inCwd(bash("git worktree add ../w"), "/home/r/other/deep")}, []string{"/home/r/other/w"}},
		{"git -C cambia la base", []step{bash("git -C /home/r/repo/sub worktree add ../w")}, []string{"/home/r/repo/w"}},
		{"git worktree list no crea", []step{bash("git worktree list")}, nil},
		{"git worktree remove no crea", []step{bash("git worktree remove ../a")}, nil},
		{"nombrado dentro de un heredoc", []step{bash("cat <<'X'\ngit worktree add ../x\nX")}, nil},
		{"fallido no cuenta", []step{failed(bash("git worktree add ../a"))}, nil},
		{"EnterWorktree con path", []step{call("EnterWorktree", map[string]any{"path": "../wt-x"})}, []string{"/home/r/wt-x"}},
		{"EnterWorktree con name", []step{call("EnterWorktree", map[string]any{"name": "feat"})}, []string{"/home/r/proj/.claude/worktrees/feat"}},
		{"EnterWorktree fallido", []step{failed(call("EnterWorktree", map[string]any{"name": "feat"}))}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := worktreePaths(scanSteps(t, Turn, c.steps...))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("worktrees = %v, quiero %v", got, c.want)
			}
		})
	}
}

func TestWorktreeAddIsAMutation(t *testing.T) {
	if !scanSteps(t, Turn, bash("git worktree add ../w")).Mutated() {
		t.Fatal("crear un worktree muta")
	}
}

func TestSaves(t *testing.T) {
	save := func(tool string) step { return call(tool, map[string]any{"content": "**Task**: X"}) }
	cases := []struct {
		name  string
		steps []step
		opts  Options
		want  []string
	}{
		{"las cuatro de neugram", []step{
			save("mcp__neugram__mem_save"), save("mcp__neugram__mem_update"),
			save("mcp__neugram__handoff_save"), save("mcp__neugram__worktree_register"),
		}, Options{}, []string{
			"mcp__neugram__mem_save", "mcp__neugram__mem_update",
			"mcp__neugram__handoff_save", "mcp__neugram__worktree_register",
		}},
		{"el plugin de engram no cuenta", []step{save("mcp__plugin_engram_engram__mem_save")}, Options{}, nil},
		{"mem_save_prompt no cuenta", []step{save("mcp__neugram__mem_save_prompt")}, Options{}, nil},
		{"sin prefijo no cuenta", []step{save("mem_save")}, Options{}, nil},
		{"mem_search no cuenta", []step{save("mcp__neugram__mem_search")}, Options{}, nil},
		{"fallida no cuenta", []step{failed(save("mcp__neugram__mem_save"))}, Options{}, nil},
		{"SaveTools reemplaza el set", []step{save("mcp__plugin_engram_engram__mem_save"), save("mcp__neugram__mem_save")},
			Options{SaveTools: []string{"mcp__plugin_engram_engram__mem_save"}},
			[]string{"mcp__plugin_engram_engram__mem_save"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			writeFile(t, path, render(c.steps))
			res, err := Scan(path, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, s := range res.Saves {
				got = append(got, s.Tool)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("saves = %v, quiero %v", got, c.want)
			}
		})
	}
}

func TestSaveKeepsInput(t *testing.T) {
	res := scanSteps(t, Turn, call("mcp__neugram__worktree_register", map[string]any{"path": "/home/r/w", "session_id": "s"}))
	if len(res.Saves) != 1 || res.Saves[0].Input["path"] != "/home/r/w" {
		t.Fatalf("%+v", res.Saves)
	}
}

func TestWorktreeWithExpansionIsUnresolved(t *testing.T) {
	res := scanSteps(t, Turn, bash("git worktree add -b x $WT Development"))
	if len(res.Worktrees) != 1 || res.Worktrees[0].Path != "" || !res.Worktrees[0].Unresolved {
		t.Fatalf("%+v", res.Worktrees)
	}
	if len(res.Mutations) != 1 || !res.Mutations[0].Worktree || !res.Mutations[0].Unresolved {
		t.Fatalf("%+v", res.Mutations)
	}
}
