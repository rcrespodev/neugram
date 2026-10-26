package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/rcrespodev/neugram/internal/client"
)

const (
	hookBudget      = 2 * time.Second
	minPromptRunes  = 10
	hooksLogName    = "hooks.log"
	unreachableCode = "daemon_unreachable"
)

// Events lists the hook names `neugram hook` accepts.
var Events = []string{"session-start", "user-prompt-submit", "stop", "subagent-stop", "session-end"}

type input struct {
	SessionID           string `json:"session_id"`
	CWD                 string `json:"cwd"`
	Source              string `json:"source"`
	Prompt              string `json:"prompt"`
	TranscriptPath      string `json:"transcript_path"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
	AgentID             string `json:"agent_id"`
	StopHookActive      bool   `json:"stop_hook_active"`
}

type outcome struct {
	stdout string
	gates  []string
	result string
}

// Run executes one hook event: it reads Claude Code's JSON from in, calls
// the daemon and prints on out only what Claude Code must see. It fails open:
// the exit code is 0 whatever happens (docs/specs/hooks.md, Fail-open).
func Run(event string, in io.Reader, out, errw io.Writer, c *client.Client) int {
	start := time.Now()
	var inp input
	if err := json.NewDecoder(in).Decode(&inp); err != nil {
		return failOpen(event, inp, start, "invalid_input: "+err.Error(), errw, nil)
	}
	log := openLog()
	defer log.close()
	c.Logf = func(f string, a ...any) { log.line("%s", fmt.Sprintf(f, a...)) }
	c.Budget = hookBudget
	c.HTTP = &http.Client{Timeout: hookBudget}

	oc, err := dispatch(event, inp, c)
	if err != nil {
		return failOpen(event, inp, start, reason(err), errw, log)
	}
	if oc.stdout != "" {
		io.WriteString(out, oc.stdout)
	}
	log.line("event=%s session=%s dur=%s gates=%s result=%s", event, inp.SessionID, since(start), strings.Join(oc.gates, ","), oc.result)
	return 0
}

func dispatch(event string, inp input, c *client.Client) (outcome, error) {
	switch event {
	case "user-prompt-submit":
		if shortPrompt(inp.Prompt) {
			return outcome{result: "skipped_short"}, nil
		}
	case "stop", "subagent-stop":
		if inp.StopHookActive {
			return outcome{result: "skipped_stop_hook_active"}, nil
		}
	case "session-start", "session-end":
	default:
		return outcome{}, fmt.Errorf("unknown event %q", event)
	}
	if err := c.EnsureDaemon(client.Policy{Replace: true}); err != nil {
		return outcome{}, &unreachable{err}
	}
	switch event {
	case "session-start":
		var r struct {
			Stdout string `json:"stdout"`
		}
		err := c.Post("/hooks/session-start", map[string]string{"session_id": inp.SessionID, "cwd": inp.CWD, "source": inp.Source}, &r)
		return outcome{stdout: r.Stdout, result: "ok"}, wrap(err)
	case "user-prompt-submit":
		err := c.Post("/prompts", map[string]string{"session_id": inp.SessionID, "content": inp.Prompt}, nil)
		return outcome{result: "saved"}, wrap(err)
	case "session-end":
		return outcome{result: "ok"}, wrap(c.Post("/sessions/"+inp.SessionID+"/end", nil, nil))
	}
	return stopEvent(event, inp, c)
}

func stopEvent(event string, inp input, c *client.Client) (outcome, error) {
	req := map[string]any{"session_id": inp.SessionID, "transcript_path": inp.TranscriptPath, "stop_hook_active": inp.StopHookActive}
	if event == "subagent-stop" {
		req["agent_transcript_path"] = inp.AgentTranscriptPath
		req["agent_id"] = inp.AgentID
	}
	var r struct {
		Decision       *string  `json:"decision"`
		Reason         string   `json:"reason"`
		Gate           *string  `json:"gate"`
		GatesEvaluated []string `json:"gates_evaluated"`
	}
	if err := c.Post("/hooks/"+event, req, &r); err != nil {
		return outcome{}, wrap(err)
	}
	oc := outcome{gates: r.GatesEvaluated, result: "pass"}
	if r.Decision != nil && *r.Decision == "block" {
		raw, _ := json.Marshal(map[string]string{"decision": "block", "reason": r.Reason})
		oc.stdout = string(raw) + "\n"
		oc.result = "block"
		if r.Gate != nil {
			oc.result += ":" + *r.Gate
		}
	}
	return oc, nil
}

func shortPrompt(p string) bool {
	n := 0
	for _, r := range p {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n < minPromptRunes
}

type unreachable struct{ err error }

func (u *unreachable) Error() string { return unreachableCode + ": " + u.err.Error() }

func wrap(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*client.APIError); ok {
		return err
	}
	return &unreachable{err}
}

func reason(err error) string {
	if e, ok := err.(*client.APIError); ok {
		return e.Code + ": " + e.Message
	}
	return err.Error()
}

func failOpen(event string, inp input, start time.Time, why string, errw io.Writer, log *hooksLog) int {
	fmt.Fprintf(errw, "neugram hook %s: %s\n", event, why)
	if log == nil {
		log = openLog()
		defer log.close()
	}
	log.line("event=%s session=%s dur=%s gates= result=fail_open reason=%q", event, inp.SessionID, since(start), why)
	return 0
}

func since(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }

type hooksLog struct{ f *os.File }

func openLog() *hooksLog {
	home, err := client.Home()
	if err != nil {
		return &hooksLog{}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return &hooksLog{}
	}
	f, err := os.OpenFile(filepath.Join(home, hooksLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &hooksLog{}
	}
	return &hooksLog{f: f}
}

func (l *hooksLog) line(format string, args ...any) {
	if l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func (l *hooksLog) close() {
	if l.f != nil {
		l.f.Close()
	}
}
