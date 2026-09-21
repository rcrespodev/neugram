# Spec — Borde con Engram

Todo lo que neugram asume de Engram, verificado contra el checkout local
`36c0819` (2026-06-14) y contra el daemon en ejecución (`engram.db` de 61 MB,
2026-09-15). `neugram doctor` revalida cada punto al arrancar.

Regla: neugram **escribe** en Engram solo por HTTP y **lee** solo por ATTACH.
Nunca escribe en `engram.db` directamente.

## Escrituras por HTTP (`http://127.0.0.1:7437`)

| Cuándo | Llamada | Body | Respuesta | Errores que importan |
|---|---|---|---|---|
| SessionStart | `POST /sessions` | `{id, project, directory}` | `201 {id, status:"created"}` | `400` si falta id o project |
| SessionEnd | `POST /sessions/{id}/end` | `{summary?}` | `200` | — |
| `mem_save`, `handoff_save` | `POST /observations` | `{session_id, type, title, content, project?, scope?, topic_key?}` | `201 {id, status:"saved"}` | `404 session not found`; `400 code=session_project_mismatch` si `project` difiere del de la sesión |
| `mem_update` | `PATCH /observations/{id}` | cualquiera de `{type, title, content, project, scope, topic_key}` | `200` con la observación | `400` si el body está vacío; `404` |
| UserPromptSubmit | `POST /prompts` | `{session_id, content}` | `201 {id}` | `400` si falta alguno |
| `mem_judge` | `POST /conflicts/judge` | `{judgment_id, relation, reason?, evidence?, confidence?, session_id?}` | `200 {relation}` | `400` si `confidence` no está en [0,1] |
| tras `mem_save` | `POST /conflicts/scan` | a confirmar en el paso 3 del roadmap | candidatos | — |
| `doctor`, `hook` | `GET /health` | — | `200` | el `version` que devuelve es fijo, no sirve para detectar la versión |

Hechos que condicionan el diseño:

- **`POST /observations` devuelve `id`, no `sync_id`.** neugram resuelve el `sync_id` con `SELECT sync_id FROM engram.observations WHERE id = ?` inmediatamente después, en la misma request. Todo insert de Engram asigna `sync_id` (`'obs-' || hex(randomblob(16))`), así que nunca es NULL.
- **El HTTP no devuelve el envelope de conflictos.** `judgment_required` y `candidates` los produce el MCP de Engram, no `POST /observations`. Para conservar la detección de conflictos neugram llama `POST /conflicts/scan` tras guardar y arma el envelope con lo que devuelve. Si el payload de `scan` no permite acotar a una observación, la primera versión omite el envelope y lo registra como deuda.
- **`session_project_mismatch`.** Una sesión de Engram tiene un `project` fijo; una observación con otro `project` es rechazada. neugram nunca necesita escribir cruzado: el control vive en su propia BD y las observaciones van al repo de la sesión. Los registros del project `tasks` no se escriben más.
- El `project` que neugram manda en `POST /sessions` debe coincidir con el que Engram calcularía para ese cwd. Cómo obtenerlo sin reimplementar la detección: `GET /project/current` (parámetro a confirmar en el paso 1 del roadmap).
- `topic_key` en `POST /observations` hace upsert silencioso si ya existe en ese project. neugram lo deja pasar solo cuando el gate `block-topic-key-collision` está apagado o el agente pasó `topic_key` explícito y no hay colisión.

## Lecturas por ATTACH

```sql
ATTACH DATABASE 'file:/home/<user>/.engram/engram.db?mode=ro' AS engram;
```

Engram usa WAL, así que el lector necesita permiso de lectura sobre
`engram.db-wal` y `engram.db-shm`; en la misma máquina y usuario lo tiene.

Tablas y columnas asumidas (verificadas en `internal/store/store.go:694-750`
de upstream y en la BD viva; sin cambios en 481 commits):

| Tabla | Columnas usadas |
|---|---|
| `observations` | `id`, `sync_id`, `session_id`, `type`, `title`, `content`, `project`, `scope`, `topic_key`, `created_at`, `updated_at`, `deleted_at` |
| `sessions` | `id`, `project`, `directory`, `started_at`, `ended_at`, `summary` |
| `user_prompts` | `id`, `sync_id`, `session_id`, `content`, `project`, `created_at` |
| tabla FTS de `observations` | nombre y columnas a confirmar en el paso 1; se usa para `mem_search` con filtros SQL |

`deleted_at IS NULL` filtra soft-deletes en toda lectura.

## Detección de rotura

`neugram doctor` (y el arranque de `serve`) comprueba:

1. `GET /health` responde en `:7437`. Si no, `mem_save` devuelve error claro; la lectura sigue funcionando.
2. `PRAGMA engram.table_info(<tabla>)` contiene cada columna de la tabla de arriba. Falta una: el daemon arranca degradado, lo escribe en el log y lo muestra en la UI.
3. Cantidad de `observation_ext` huérfanas (sin fila en `engram.observations`).
4. `engram version` del binario, para el registro (el `/health` no sirve).

## Lo que no se usa

`GET /search` (topea en 20, sin offset), `GET /observations` (ignora `type`,
`topic_key`, `session_id`, fechas), `GET /context`, `/export`, `/import`,
`/sync/*`. Con el ATTACH todo eso sale por SQL sin topes. Las rarezas de esos
endpoints están documentadas en `~/.claude/docs/engram-api.md`; solo importan
para `import-legacy`, que sí usa `GET /observations?project=tasks&limit=2000`.
