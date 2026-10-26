package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/rcrespodev/neugram/internal/client"
	"io"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/rcrespodev/neugram/internal/store"
)

const pageSize = store.MaxLimit

func runTask(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "ls":
		return runTaskLs(args[1:], stdout, stderr)
	case "show":
		return runTaskShow(args[1:], stdout, stderr)
	case "wt":
		return runTaskWt(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "neugram: unknown task command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runProduct(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "ls" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("product ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseArgs(fs, args[1:], 0); err != nil {
		return 2
	}
	var products []store.Product
	err := withDaemon(func(c *client.Client) error {
		var err error
		products, err = fetchAll[store.Product](c, "/products", url.Values{}, "products")
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return printJSON(stdout, map[string]any{"total": len(products), "products": products})
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PRODUCTO\tABIERTAS\tTAREAS\tDESCRIPCIÓN")
	for _, p := range products {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", p.Name, p.OpenTasksCount, p.TasksCount, deref(p.Description))
	}
	tw.Flush()
	fmt.Fprintf(stdout, "%d productos\n", len(products))
	return 0
}

func runTaskLs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	status := fs.String("status", "", "only this status (comma-separated for several)")
	product := fs.String("product", "", "only tasks of this product")
	parent := fs.String("parent", "", "only the direct children of this task")
	all := fs.Bool("all", false, "include done tasks")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseArgs(fs, args, 0); err != nil {
		return 2
	}
	q := url.Values{}
	for name, v := range map[string]string{"status": *status, "product": *product, "parent": *parent} {
		if v != "" {
			q.Set(name, v)
		}
	}
	if *all {
		q.Set("include_done", "true")
	}
	var tasks []store.TaskSummary
	err := withDaemon(func(c *client.Client) error {
		var err error
		tasks, err = fetchAll[store.TaskSummary](c, "/tasks", q, "tasks")
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return printJSON(stdout, map[string]any{"total": len(tasks), "tasks": tasks})
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TAREA\tSTATUS\tPRODUCTO\tTÍTULO")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.TaskKey, t.Status, orDash(t.Product), t.Title)
	}
	tw.Flush()
	fmt.Fprintf(stdout, "%d tareas\n", len(tasks))
	return 0
}

func runTaskShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return 2
	}
	var d store.TaskDetail
	err = withDaemon(func(c *client.Client) error {
		return c.Get("/tasks/"+url.PathEscape(pos[0]), &d)
	})
	if err != nil {
		return fail(stderr, err)
	}
	writeTaskDetail(stdout, d)
	return 0
}

func runTaskWt(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task wt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return 2
	}
	var wts []store.Worktree
	err = withDaemon(func(c *client.Client) error {
		var err error
		wts, err = fetchAll[store.Worktree](c, "/worktrees", url.Values{"task_id": {pos[0]}}, "worktrees")
		return err
	})
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		return printJSON(stdout, map[string]any{"total": len(wts), "worktrees": wts})
	}
	writeWorktrees(stdout, wts, "")
	if len(wts) == 0 {
		fmt.Fprintf(stdout, "%s no tiene worktrees registrados\n", pos[0])
	}
	return 0
}

func writeTaskDetail(w io.Writer, d store.TaskDetail) {
	t := d.Task
	fmt.Fprintf(w, "%s — %s\n", t.TaskKey, t.Title)
	fmt.Fprintf(w, "  status     %s\n", t.Status)
	fmt.Fprintf(w, "  producto   %s\n", orDash(t.Product))
	if len(t.AncestorKeys) > 0 {
		fmt.Fprintf(w, "  padre      %s\n", strings.Join(t.AncestorKeys, " › "))
	}
	if t.Tracker != nil {
		fmt.Fprintf(w, "  tracker    %s\n", strings.Join(nonEmpty(t.Tracker.Kind, t.Tracker.ID, t.Tracker.URL), " · "))
	}
	if t.Notes != nil {
		fmt.Fprintf(w, "  notas      %s\n", strings.ReplaceAll(*t.Notes, "\n", "\n             "))
	}
	fmt.Fprintf(w, "  creada     %s · actualizada %s\n", t.CreatedAt, t.UpdatedAt)

	if d.Children != nil && len(*d.Children) > 0 {
		fmt.Fprintf(w, "\nsub-tareas (%d)\n", len(*d.Children))
		writeNodes(w, *d.Children, "  ")
	}

	fmt.Fprintf(w, "\nsesiones (%d)\n", len(d.Sessions))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range d.Sessions {
		ended := "abierta"
		if s.EndedAt != nil {
			ended = *s.EndedAt
		}
		fmt.Fprintf(tw, "  %s\t%s → %s\t%s\t%s\tpin:%s\n", s.SessionID, s.StartedAt, ended, s.Project, s.Directory, orDash(s.PinnedBy))
	}
	tw.Flush()

	alive := 0
	for _, wt := range d.Worktrees {
		if wt.Alive {
			alive++
		}
	}
	fmt.Fprintf(w, "\nworktrees (%d, %d vivos)\n", len(d.Worktrees), alive)
	writeWorktrees(w, d.Worktrees, "  ")

	fmt.Fprintf(w, "\nhandoffs (%d)\n", len(d.Handoffs))
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, h := range d.Handoffs {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", h.CreatedAt, h.SyncID, h.Title)
	}
	tw.Flush()

	fmt.Fprintf(w, "\nprogreso (%d)\n", len(d.ProgressReports))
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, p := range d.ProgressReports {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", p.CreatedAt, p.DelegationID, p.Role, p.Status, p.Title)
	}
	tw.Flush()

	fmt.Fprintf(w, "\nobservaciones (últimas %d)\n", len(d.Observations))
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, o := range d.Observations {
		title := o.Title
		if !strings.EqualFold(o.TaskKey, t.TaskKey) {
			title += " (" + o.TaskKey + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", o.CreatedAt, o.Type, o.Project, o.SyncID, title)
	}
	tw.Flush()
}

func writeNodes(w io.Writer, nodes []store.TaskNode, indent string) {
	for _, n := range nodes {
		fmt.Fprintf(w, "%s- %s [%s] %s (%d obs)\n", indent, n.TaskKey, n.Status, n.Title, n.ObservationsCount)
		writeNodes(w, n.Children, indent+"  ")
	}
}

func writeWorktrees(w io.Writer, wts []store.Worktree, indent string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, wt := range wts {
		state := "muerto"
		if wt.Alive {
			state = "vivo"
		}
		branch := wt.Branch
		if wt.Base != nil {
			branch += " ← " + *wt.Base
		}
		fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", indent, state, wt.Path, branch, deref(wt.Purpose))
	}
	tw.Flush()
}

// fetchAll pages through a listing endpoint until it has every row.
func fetchAll[T any](c *client.Client, path string, q url.Values, field string) ([]T, error) {
	out := []T{}
	for {
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(len(out)))
		var page map[string]json.RawMessage
		if err := c.Get(path+"?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		var rows []T
		var total int
		if err := json.Unmarshal(page[field], &rows); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(page["total"], &total); err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) == 0 || len(out) >= total {
			return out, nil
		}
	}
}

func withDaemon(fn func(*client.Client) error) error {
	c := newClient()
	if err := c.EnsureDaemon(client.Policy{Replace: true}); err != nil {
		return err
	}
	return fn(c)
}

// parseArgs lets flags follow the positional arguments, as in `task wt KEY --json`.
func parseArgs(fs *flag.FlagSet, args []string, positional int) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) != positional {
		err := fmt.Errorf("want %d argument(s), got %d", positional, len(pos))
		fmt.Fprintf(fs.Output(), "neugram %s: %v\n", fs.Name(), err)
		return nil, err
	}
	return pos, nil
}

func fail(stderr io.Writer, err error) int {
	var ae *client.APIError
	if errors.As(err, &ae) {
		fmt.Fprintf(stderr, "neugram: %s: %s\n", ae.Code, ae.Error())
		return 1
	}
	fmt.Fprintf(stderr, "neugram: %v\n", err)
	return 1
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
