package server

import (
	"net/http"
	"strings"

	"github.com/rcrespodev/neugram/internal/hooks"
	"github.com/rcrespodev/neugram/internal/store"
)

type toolLine struct{ name, line string }

var flowTools = []toolLine{
	{"mem_save", "guarda una observación (decisión, bug, hallazgo); el pie **Task** lo agrega neugram"},
	{"mem_search", "busca observaciones por texto, con filtros de tipo, proyecto y tarea"},
	{"mem_update", "corrige una observación existente por sync_id"},
	{"mem_judge", "resuelve un candidato de conflicto que devolvió mem_save"},
	{"task_pin", "fija la tarea de la sesión (o none); con title crea la tarea"},
	{"task_list", "lista tareas con producto, status, actividad y worktrees vivos"},
	{"task_status", "cambia el status de una tarea: todo, in-progress, review, blocked, done"},
	{"task_show", "ficha completa de una tarea: jerarquía, sesiones, worktrees, handoffs"},
	{"worktree_register", "registra un git worktree recién creado en la tarea"},
	{"handoff_save", "guarda el handoff de cierre de la sesión; repetirlo lo reemplaza"},
}

func (s *Server) routesInstructions() {
	s.mux.HandleFunc("GET /instructions", s.getInstructions)
}

func (s *Server) getInstructions(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	rendered, err := boolParam(v.Get("rendered"), true, "rendered")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	product := v.Get("product")
	if product != "" {
		if err := s.requireProduct(r, product); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	overrides, err := s.gateOverrides(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	generated := renderGenerated(overrides)
	body := map[string]any{"product": nilIfEmpty(product), "source": "global", "generated": generated, "free": "", "updated_at": nil}
	if rendered {
		body["rendered"] = generated
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) requireProduct(r *http.Request, name string) error {
	products, _, err := s.st.ListProducts(r.Context(), store.ProductQuery{Q: name, Limit: store.MaxLimit})
	if err != nil {
		return err
	}
	for _, p := range products {
		if strings.EqualFold(p.Name, name) {
			return nil
		}
	}
	return &store.Error{Code: "unknown_product", Message: "product " + name + " does not exist"}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func renderGenerated(overrides map[string]hooks.Override) string {
	var b strings.Builder
	b.WriteString("## Tools de flujo\n")
	b.WriteString("Toda tool que escribe pide `session_id`: es el de la primera línea que imprime `session-start` (`CLAUDE_CODE_SESSION_ID=<id>`).\n")
	for _, t := range flowTools {
		b.WriteString("- " + t.name + ": " + t.line + "\n")
	}
	b.WriteString("\n## Gates habilitados\n")
	for _, g := range hooks.EnabledGates(overrides) {
		b.WriteString("- " + g.Name + ": " + g.Phrase + "\n")
	}
	b.WriteString("\n## Tipos de observación\n")
	b.WriteString(strings.Join(store.ObservationTypes, " · ") + "\n")
	b.WriteString("\n## Pie de tarea\n")
	b.WriteString("Toda observación termina con `**Task**: <KEY> — <título>`, o `**Task**: ninguna — <motivo>` sin tarea. neugram lo escribe solo; si lo incluís, respetá ese formato.\n")
	return b.String()
}
