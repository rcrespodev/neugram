package legacy

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Summary is what an import did, or would do on a dry run, in the shape of
// docs/specs/import-legacy.md (Salida).
type Summary struct {
	DryRun bool
	Checks []Check

	Sessions, Observations, ControlRecords, Prompts int
	Relations, RelationsJudged, RelationsPending    int
	RelationsSkipped                                []string

	Tasks, TasksWithIdentity, Epics  int
	TasksWithoutIdentity             []string
	ParentsFromHint, ParentsFromRule int
	ParentHintsUnresolved            []string
	ParentsDropped                   []string
	StatusWarnings                   []string

	Products   int
	ProductMap map[string]string

	Pins, PinsWithTask, PinsNone, PinsSuperseded int
	PinsMissingSession                           []string

	Worktrees, WorktreesAlive, WorktreesDead, WorktreesOverwritten int
	WorktreesUnparseable                                           []string

	Handoffs           int
	HandoffsSkipped    []string
	Progress           int
	ProgressSuperseded int
	ProgressSkipped    []string

	Footers, FootersWithTask, FootersNone int
	FootersUnresolved                     []string

	PlainImported  []string
	DeletedControl []string

	HashChecked, HashMismatches int

	TimestampsNormalized, TimestampsInvalid int
}

func (s *Summary) addProduct(original, name string) {
	if s.ProductMap == nil {
		s.ProductMap = map[string]string{}
	}
	s.ProductMap[original] = name
}

func (s *Summary) WriteChecks(w io.Writer) {
	fmt.Fprintln(w, "verificaciones de procedencia")
	for _, c := range s.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  %s %-45s %s\n", mark, c.Name, c.Detail)
	}
}

func (s *Summary) Write(w io.Writer) {
	line := func(label string, n int, detail string, args ...any) {
		fmt.Fprintf(w, "%-13s %6s   %s\n", label, fmtInt(n), fmt.Sprintf(detail, args...))
	}
	more := func(detail string, args ...any) {
		fmt.Fprintf(w, "%22s %s\n", "", fmt.Sprintf(detail, args...))
	}

	line("sesiones", s.Sessions, "")
	line("observaciones", s.Observations, "(%s registros de control no importados como observación)",
		fmtInt(s.ControlRecords-s.Handoffs-len(s.PlainImported)))
	line("prompts", s.Prompts, "")
	line("relaciones", s.Relations, "(%s juzgadas, %s pendientes) · %d omitidas por apuntar a registros de control",
		fmtInt(s.RelationsJudged), fmtInt(s.RelationsPending), len(s.RelationsSkipped))
	line("tareas", s.Tasks, "%d con identity · %d creadas sin identity · %d como épico",
		s.TasksWithIdentity, len(s.TasksWithoutIdentity), s.Epics)
	more("%d con padre por indicio en **Product** · %d por regla de producto", s.ParentsFromHint, s.ParentsFromRule)
	if len(s.ParentHintsUnresolved) > 0 {
		more("%d indicios de padre a una key sin tarea, sin padre: %s", len(s.ParentHintsUnresolved), list(s.ParentHintsUnresolved))
	}
	if len(s.ParentsDropped) > 0 {
		more("%d padres descartados por tope de %d niveles o ciclo: %s", len(s.ParentsDropped), maxTaskLevels, list(s.ParentsDropped))
	}
	if len(s.StatusWarnings) > 0 {
		more("%d status fuera del enum, quedan en todo: %s", len(s.StatusWarnings), list(s.StatusWarnings))
	}
	line("productos", s.Products, "(desde %d cadenas; mapeo abajo)", len(s.ProductMap))
	line("pins", s.Pins, "(%d con tarea, %d none) · %d reemplazados por un pin más nuevo de la misma sesión · %d sin sesión",
		s.PinsWithTask, s.PinsNone, s.PinsSuperseded, len(s.PinsMissingSession))
	line("worktrees", s.Worktrees, "(%d vivos, %d muertos) · %d registro(s) sin path parseable · %d reemplazados por uno más nuevo con el mismo path",
		s.WorktreesAlive, s.WorktreesDead, len(s.WorktreesUnparseable), s.WorktreesOverwritten)
	line("handoffs", s.Handoffs, "vinculados a tarea y sesión · %d sin sesión", len(s.HandoffsSkipped))
	line("progress", s.Progress, "reportes en progress_reports · %d reemplazados por uno más nuevo de la misma sesión y slot · %d no importables",
		s.ProgressSuperseded, len(s.ProgressSkipped))
	line("pies", s.Footers, "%s con tarea · %s ninguna · %d sin key resoluble",
		fmtInt(s.FootersWithTask), fmtInt(s.FootersNone), len(s.FootersUnresolved))
	line("tasks→obs", len(s.PlainImported), "registros del project tasks sin forma de control, importados como observación (lista abajo)")
	if len(s.DeletedControl) > 0 {
		more("%d registros de control borrados, ignorados: %s", len(s.DeletedControl), list(s.DeletedControl))
	}
	line("hash", s.HashChecked, "normalized_hash recomputado · %d discrepancias", s.HashMismatches)
	line("timestamps", s.TimestampsNormalized, "normalizados a 2006-01-02T15:04:05Z · %d sin parsear", s.TimestampsInvalid)

	if len(s.TasksWithoutIdentity) > 0 {
		fmt.Fprintf(w, "\nsin identity: %s\n", strings.Join(s.TasksWithoutIdentity, ", "))
	}
	if len(s.ProductMap) > 0 {
		fmt.Fprintln(w, "\nproductos (cadena original → producto)")
		originals := make([]string, 0, len(s.ProductMap))
		for o := range s.ProductMap {
			originals = append(originals, o)
		}
		sort.Strings(originals)
		for _, o := range originals {
			fmt.Fprintf(w, "  %s → %s\n", o, s.ProductMap[o])
		}
	}
	for _, sec := range []struct {
		title string
		items []string
	}{
		{"tasks→obs: importados como observación con project tasks", s.PlainImported},
		{"relaciones omitidas", s.RelationsSkipped},
		{"pins sin sesión", s.PinsMissingSession},
		{"worktrees sin path parseable", s.WorktreesUnparseable},
		{"handoffs sin sesión", s.HandoffsSkipped},
		{"progress no importables", s.ProgressSkipped},
		{"pies sin key resoluble (sync_id)", s.FootersUnresolved},
	} {
		if len(sec.items) > 0 {
			fmt.Fprintf(w, "\n%s\n  %s\n", sec.title, strings.Join(sec.items, "\n  "))
		}
	}
}

func list(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, ", ")
}

// fmtInt groups thousands with a dot, as the spec's sample output does.
func fmtInt(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + fmtInt(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}
