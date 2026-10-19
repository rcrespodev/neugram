// Package cli dispatches the neugram subcommands.
package cli

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Version is set at build time with -ldflags "-X github.com/rcrespodev/neugram/internal/cli.Version=...".
var Version = "dev"

const usage = `usage: neugram <command>

commands:
  import-legacy [--dry-run] [--engram-db PATH] [--db PATH]
                import ~/.engram/engram.db into neugram.db
  doctor [--db PATH]
                check the health of neugram.db
  version       print the build version
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
