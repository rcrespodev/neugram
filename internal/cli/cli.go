// Package cli dispatches the neugram subcommands.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/rcrespodev/neugram/internal/hooks"
	"github.com/rcrespodev/neugram/internal/mcp"
)

// Version is set at build time with -ldflags "-X github.com/rcrespodev/neugram/internal/cli.Version=...".
var Version = "dev"

const usage = `usage: neugram <command>

commands:
  serve [--addr 127.0.0.1:7438] [--db PATH]
                run the daemon, the only process that opens neugram.db
  task ls [--status S] [--product P] [--parent KEY] [--all] [--json]
                list tasks; open ones unless --all or --status
  task show KEY
                task card: sessions, worktrees, handoffs, progress, observations
  task wt KEY [--json]
                worktrees of a task, alive or dead
  product ls [--json]
                list products with their task counts
  mcp           serve MCP tools over stdio as a thin client of the daemon;
                starts the daemon if none answers, never replaces a live one
  hook EVENT    run a Claude Code hook (session-start, user-prompt-submit, stop,
                subagent-stop, session-end): JSON on stdin, fail-open, logs to hooks.log
  import-legacy [--dry-run] [--engram-db PATH] [--db PATH]
                import ~/.engram/engram.db into neugram.db
  doctor [--db PATH]
                check the health of neugram.db
  version       print the build version

The daemon listens on $NEUGRAM_ADDR (default 127.0.0.1:7438) and keeps its
database and serve.log under $NEUGRAM_HOME (default ~/.neugram).
`

// Run executes the subcommand named in args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, versionLine())
		return 0
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "task":
		return runTask(args[1:], stdout, stderr)
	case "product":
		return runProduct(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(stderr)
	case "hook":
		return runHook(args[1:], stderr)
	case "import-legacy":
		return runImportLegacy(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "neugram: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func versionLine() string {
	return fmt.Sprintf("neugram %s (%s, %s/%s)", Version, revision(), runtime.GOOS, runtime.GOARCH)
}

func revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	return rev + dirty
}

func runHook(args []string, stderr io.Writer) int {
	if len(args) != 1 || !slices.Contains(hooks.Events, args[0]) {
		fmt.Fprintf(stderr, "usage: neugram hook {%s}\n", strings.Join(hooks.Events, "|"))
		return 2
	}
	return hooks.Run(args[0], os.Stdin, os.Stdout, stderr, newClient())
}

// runMCP keeps stdout for the protocol; notices go to stderr.
func runMCP(stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := newClient()
	c.Logf = func(format string, args ...any) { fmt.Fprintf(stderr, "neugram mcp: "+format+"\n", args...) }
	if err := mcp.New(c).Run(ctx, os.Stdin, os.Stdout, buildVersion()); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "neugram mcp: %v\n", err)
		return 1
	}
	return 0
}
