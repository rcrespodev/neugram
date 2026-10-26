package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rcrespodev/neugram/internal/store"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

// errorSpec is one row of the catalog in docs/specs/api.md "Errores".
type errorSpec struct {
	status    int
	hint      string
	openTasks bool
}

var errorCatalog = map[string]errorSpec{
	"no_pin":                 {http.StatusUnprocessableEntity, `task_pin {"task_id": "<KEY>"} o task_pin {"task_id": "none"}`, true},
	"unknown_task":           {http.StatusNotFound, "task_pin con title para crearla, o neugram task ls", true},
	"task_exists":            {http.StatusConflict, "neugram task edit <key> para cambiar título o producto", false},
	"invalid_parent":         {http.StatusUnprocessableEntity, "el padre debe existir y estar en el nivel 1 o 2; neugram task ls", false},
	"unknown_product":        {http.StatusNotFound, `neugram product add "<nombre>"`, false},
	"product_exists":         {http.StatusConflict, "neugram product ls", false},
	"unknown_session":        {http.StatusNotFound, "la sesión la crea el hook session-start y el session_id es el de su primera línea; si el hook no corrió, recargá la sesión", false},
	"not_found":              {http.StatusNotFound, "listá los válidos con la consulta del recurso", false},
	"invalid_request":        {http.StatusBadRequest, "revisá el parámetro nombrado en message", false},
	"invalid_type":           {http.StatusUnprocessableEntity, "type ∈ " + strings.Join(store.ObservationTypes, " · "), false},
	"invalid_enum":           {http.StatusUnprocessableEntity, "status ∈ " + strings.Join(store.TaskStatuses, " · "), false},
	"required_field":         {http.StatusUnprocessableEntity, "completá el campo nombrado en message; neugram schema ls", false},
	"invalid_field":          {http.StatusUnprocessableEntity, "neugram schema ls --table <t> lista las columnas", false},
	"invalid_report":         {http.StatusUnprocessableEntity, "summary de 5 a 10, next_steps de 3 a 6 y refs.files como path:line", false},
	"topic_key_collision":    {http.StatusConflict, "mem_update con el sync_id existente, que viene en details", false},
	"field_exists":           {http.StatusConflict, "neugram schema ls --table <t>", false},
	"reserved_name":          {http.StatusUnprocessableEntity, "id, sync_id, created_at, updated_at, deleted_at y las columnas fijas están reservados", false},
	"field_in_use":           {http.StatusConflict, "--yes --force; details trae el conteo de filas", false},
	"import_precheck_failed": {http.StatusUnprocessableEntity, "corregí la verificación nombrada en message", false},
	"internal":               {http.StatusInternalServerError, "", false},
}

// fail writes the error envelope of docs/specs/api.md. Anything that is not
// a catalogued store.Error becomes internal.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *store.Error
	var spec errorSpec
	var catalogued bool
	if errors.As(err, &se) {
		spec, catalogued = errorCatalog[se.Code]
	}
	if !catalogued {
		s.logInternal(r, err)
		writeJSON(w, http.StatusInternalServerError, map[string]apiError{"error": {
			Code: "internal", Message: err.Error(), Hint: "mirá " + s.opts.LogPath,
		}})
		return
	}
	e := apiError{Code: se.Code, Message: se.Message, Hint: spec.hint}
	if spec.openTasks {
		e.Details = s.openTasks(r)
	}
	writeJSON(w, spec.status, map[string]apiError{"error": e})
}

func (s *Server) openTasks(r *http.Request) any {
	open, _, err := s.st.ListTasks(r.Context(), store.TaskQuery{Limit: store.MaxLimit})
	if err != nil {
		return nil
	}
	names := make([]string, len(open))
	for i, t := range open {
		names[i] = t.TaskKey + " — " + t.Title
	}
	return map[string]any{"open_tasks": names}
}

func (s *Server) logInternal(r *http.Request, err error) {
	if s.opts.LogPath == "" {
		return
	}
	f, ferr := os.OpenFile(s.opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s serve %s %s internal: %v\n", time.Now().UTC().Format(store.TimeLayout), r.Method, r.URL.Path, err)
}
