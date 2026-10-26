# Spec — API

Fuente única de la superficie de API de neugram. Tres superficies, un proceso,
un puerto: las tools MCP que ve el agente, el HTTP de `neugram serve` en
`:7438` que consumen el MCP, el CLI, los hooks y la UI web, y el catálogo de
errores común a las dos.

No hay un segundo daemon ni un segundo puerto: el núcleo de memoria es código
propio, escrito contra `specs/data-model.md` y no copiado de Engram (ADR 0011,
`specs/engram-provenance.md`). Todo lo que acá se describe lo resuelve `serve`
contra `~/.neugram/neugram.db`.

Procedencia de cada método: **E** = heredado de Engram (mismo nombre y mismo
contrato hacia el agente, para que los skills y el CLAUDE.md migren sin
reaprender nada) · **N** = propio de neugram.

## Índice

### Tools MCP (`neugram mcp`, stdio) — 19 (10 en el paso 3, 9 en el 4)

| Tool | Proc. | Qué hace | Paso |
|---|---|---|---|
| [`mem_save`](#mem_save) | E | Guarda una observación | 3 |
| [`mem_update`](#mem_update) | E | Modifica una observación existente | 3 |
| [`mem_search`](#mem_search) | E | Búsqueda FTS con filtros por tarea, repo, tipo y columnas | 3 |
| [`mem_context`](#mem_context) | E | Contexto reciente del repo o de la tarea | 4 |
| [`mem_get_observation`](#mem_get_observation) | E | Una observación entera por `sync_id` | 4 |
| [`mem_save_prompt`](#mem_save_prompt) | E | Guarda un prompt del usuario (compatibilidad) | 4 |
| [`mem_judge`](#mem_judge) | E | Juzga un candidato de conflicto | 3 |
| [`mem_session_summary`](#mem_session_summary) | E | Resumen de cierre cuando no hay handoff | 4 |
| [`mem_timeline`](#mem_timeline) | E | Observaciones vecinas de la misma sesión | 4 |
| [`mem_review`](#mem_review) | E | Lista o marca revisadas las observaciones vencidas | 4 |
| [`mem_current_project`](#mem_current_project) | E | Repo de la sesión, producto y tarea pineada | 4 |
| [`task_pin`](#task_pin) | N | Fija la tarea de la sesión; crea la tarea si hace falta | 3 |
| [`task_list`](#task_list) | N | Tareas con producto, status, sesiones y worktrees | 3 |
| [`task_status`](#task_status) | N | Cambia el status de una tarea | 3 |
| [`task_show`](#task_show) | N | Ficha completa de una tarea | 3 |
| [`worktree_register`](#worktree_register) | N | Registra un `git worktree` de la tarea | 3 |
| [`handoff_save`](#handoff_save) | N | Guarda el handoff de cierre de sesión | 3 |
| [`progress_write`](#progress_write) | N | Reporte de progreso de un subagente | 4 |
| [`progress_list`](#progress_list) | N | Lee reportes de progreso | 4 |

### HTTP del daemon (`http://127.0.0.1:7438`) — 44

| Método y ruta | Proc. | Consumidor |
|---|---|---|
| [`GET /health`](#get-health) | N | hooks, CLI |
| [`POST /shutdown`](#post-shutdown) | N | hooks, MCP, CLI |
| [`GET /project/current`](#get-projectcurrent) | E | MCP, CLI |
| [`POST /observations`](#post-observations) | E | MCP |
| [`GET /observations`](#get-observations) | E | MCP, UI |
| [`GET /observations/{sync_id}`](#get-observationssync_id) | E | MCP, UI |
| [`PATCH /observations/{sync_id}`](#patch-observationssync_id) | E | MCP, UI |
| [`DELETE /observations/{sync_id}`](#delete-observationssync_id) | E | UI |
| [`GET /observations/{sync_id}/timeline`](#get-observationssync_idtimeline) | E | MCP, UI |
| [`POST /prompts`](#post-prompts) | E | hook, MCP |
| [`GET /prompts`](#get-prompts) | E | UI |
| [`GET /review`](#get-review) | E | MCP, UI |
| [`POST /review/mark_reviewed`](#post-reviewmark_reviewed) | E | MCP, UI |
| [`GET /conflicts`](#get-conflicts) | E | UI |
| [`POST /conflicts/judge`](#post-conflictsjudge) | E | MCP, UI |
| [`POST /conflicts/scan`](#post-conflictsscan) | E | UI, CLI |
| [`POST /sessions`](#post-sessions) | E | hook |
| [`GET /sessions/{id}`](#get-sessionsid) | E | UI, MCP |
| [`POST /sessions/{id}/pin`](#post-sessionsidpin) | N | MCP, CLI, UI |
| [`PUT /sessions/{id}/handoff`](#put-sessionsidhandoff) | N | MCP |
| [`POST /sessions/{id}/end`](#post-sessionsidend) | E | hook |
| [`GET /tasks`](#get-tasks) | N | MCP, CLI, UI |
| [`POST /tasks`](#post-tasks) | N | MCP, CLI |
| [`GET /tasks/{key}`](#get-taskskey) | N | MCP, CLI, UI |
| [`PATCH /tasks/{key}`](#patch-taskskey) | N | CLI, UI |
| [`PUT /tasks/{key}/status`](#put-taskskeystatus) | N | MCP, CLI, UI |
| [`GET /products`](#get-products) | N | CLI, UI |
| [`POST /products`](#post-products) | N | CLI |
| [`GET /worktrees`](#get-worktrees) | N | MCP, CLI, UI |
| [`POST /worktrees`](#post-worktrees) | N | MCP, CLI |
| [`GET /progress`](#get-progress) | N | MCP, UI |
| [`POST /progress`](#post-progress) | N | MCP |
| [`GET /schema/fields`](#get-schemafields) | N | CLI, UI, MCP |
| [`POST /schema/fields`](#post-schemafields) | N | CLI |
| [`DELETE /schema/fields/{table}/{name}`](#delete-schemafieldstablename) | N | CLI |
| [`GET /instructions`](#get-instructions) | N | hook, CLI, UI |
| [`PUT /instructions`](#put-instructions) | N | CLI |
| [`GET /gates`](#get-gates) | N | CLI, UI |
| [`PUT /gates/{name}`](#put-gatesname) | N | CLI |
| [`POST /import/legacy`](#post-importlegacy) | N | CLI |
| [`GET /doctor`](#get-doctor) | N | CLI, UI |
| [`POST /hooks/session-start`](#post-hookssession-start) | N | hook |
| [`POST /hooks/stop`](#post-hooksstop) | N | hook |
| [`POST /hooks/subagent-stop`](#post-hookssubagent-stop) | N | hook |

Reparto por paso: las tools marcadas **3** y las rutas que usan entran en el
paso 3; las marcadas **4** entran antes del corte (`roadmap.md`). Las rutas
que solo consume una tool del paso 4 —`GET /observations/{sync_id}/timeline`,
`GET /review`, `POST /review/mark_reviewed`, `GET /progress` y
`POST /progress`— se implementan con ella. La spec de las del paso 4 queda
completa en este documento; lo que cambia es cuándo existen. `mem_context` no
tiene ruta HTTP propia todavía: se define con la tool, en el paso 4.

## Convenciones

Valen para las dos superficies salvo donde se aclara.

- **`task_id` acepta la `key` humana.** Cualquier parámetro llamado `task_id`
  toma la `key` (`TRIPULSE-3652`, `engram-fork-debat`, sin distinguir
  mayúsculas), el literal `none`, o el ULID de `tasks.id`. Si se omite, se usa
  el pin de la sesión. Sin pin y sin `task_id`, la llamada falla con
  [`no_pin`](#no_pin), cuyo `hint` nombra `task_pin`. Los parámetros marcados
  con **key** en las tablas de payload son los que aceptan esta forma.
- **En la respuesta, `task_id` es el ULID y `task_key` la `key`.** Los dos
  viajan siempre juntos; ninguna respuesta obliga a resolver uno desde el otro.
  Una observación sin tarea devuelve los dos en `null`; un pin a `none`
  devuelve `task_id: null` con `task_key: "none"`.
- **`sync_id` es la clave pública** de una observación, un prompt y una
  relación. `observations.id` y `user_prompts.id` son el `content_rowid` que
  FTS5 exige y **no aparecen en ninguna respuesta** — el envelope heredado de
  Engram traía `id`, y esa clave desaparece en neugram. Prefijos:
  `obs-<32 hex>`, `prm-<32 hex>`, `rel-<32 hex>`.
- **`session_id` es un argumento explícito de las tools, nunca del entorno.**
  Es obligatorio en toda tool que escribe (`mem_save`, `mem_update`,
  `mem_judge`, `task_pin`, `task_status`, `worktree_register`, `handoff_save`
  y, en el paso 4, `mem_save_prompt`, `mem_session_summary`, `progress_write`
  y `mem_review` con `mark_reviewed`) y opcional en las lecturas, donde solo
  sirve para el pin y el `project` por defecto. El proceso MCP **no** lee
  `CLAUDE_CODE_SESSION_ID` del entorno y no hay fallback: sobrevive a `/clear`,
  que cambia de sesión sin cambiar de proceso, así que un id heredado del
  entorno cae en la sesión anterior con su pin. El valor sale de la primera
  línea que imprime `session-start` (`CLAUDE_CODE_SESSION_ID=<id>`) en todo
  `source`. Las lecturas por `sync_id` (`mem_get_observation`,
  `mem_timeline`) no lo toman. En HTTP es un campo obligatorio del body de
  toda escritura, o el `{id}` de la ruta.
- **Un `session_id` sin fila falla con [`unknown_session`](#unknown_session)**;
  ni el MCP ni el daemon crean sesiones al vuelo. La crea el hook
  `session-start`; si no corrió (daemon caído al arrancar), el `hint` pide
  recargar la sesión en vez de inventar una con un `directory` supuesto.
- **Pie `**Task**` con pin `none`.** Si el content ya trae un pie
  `**Task**: ninguna — <por qué>`, se respeta. Si falta, el daemon escribe
  `**Task**: ninguna`, porque el motivo solo lo sabe quien escribe.
- **`where`** es un objeto `{columna: valor}` sobre columnas fijas o custom de
  la tabla consultada. Igualdad exacta; `null` filtra `IS NULL`; sin
  operadores en la primera versión. Una columna inexistente falla con
  [`invalid_field`](#invalid_field).
- **Campos custom.** Cada fila de `schema_fields` aparece como propiedad de
  primer nivel en el JSON Schema de la tool que corresponde —`mem_save` /
  `mem_update` para `observations`, `task_pin` / `task_list` / `task_show`
  para `tasks`, `worktree_register` para `worktrees`— con `description` y
  `enum` tomados de la fila, y `required=1` la hace obligatoria en `mem_save`.
  En las respuestas viajan al mismo nivel que las columnas fijas. `schema add`
  regenera el JSON Schema en el momento; una sesión MCP abierta lo ve en su
  próximo `tools/list`.
- **Timestamps** ISO 8601 UTC con `Z`, como los escribe `internal/timeutil`.
- **`project`** es el repo, derivado por `internal/project` (git remote
  → raíz git → basename). Cuando una tool lo acepta y no se pasa, vale
  `sessions.project` de la sesión: el MCP no conoce el cwd vivo del agente. No es el producto: el producto vive en `products` y
  cuelga de la tarea.
- **Soft delete.** Toda lectura filtra `deleted_at IS NULL`; no hay parámetro
  para incluir borradas.
- **Jerarquía de tareas.** `tasks.parent_task_id` admite tres niveles: épico,
  tarea y sub-tarea. Son niveles, no tipos: una tarea de cualquier nivel es
  una tarea como cualquier otra para el pin, el status y el tracker.
  **`task_id` sube el árbol y no lo baja**: filtrar por una sub-tarea en
  `mem_search` o `mem_context` devuelve además las observaciones de su padre y
  su abuelo (`include_ancestors`, default `true`), porque el contexto del
  épico es lo que le falta a una sesión pineada abajo. Bajar es opt-in
  (`include_subtasks`, default `false`) y entonces es el subárbol entero, no
  las hijas directas; [`task_show`](#task_show) baja siempre. En todos los
  casos cada observación viaja con el `task_key` de su tarea, para que se vea
  de cuál es. El DDL, los triggers de profundidad y las reglas de propagación
  están en `specs/data-model.md`.
- **Los ancestros viajan sólo como `key`.** Es la única excepción al par
  `task_id` + `task_key`: ningún método identifica al padre por ULID —`parent`
  toma la `key`, y `none` lo desengancha— así que el ULID del padre no
  referencia nada que el cliente pueda pedir. `parent_key` es el padre
  inmediato; `ancestor_keys` es la cadena completa del épico hacia abajo sin
  incluir la tarea misma, y es `[]` en un épico.
- **La API no borra tareas.** No hay `DELETE /tasks/{key}` ni tool
  equivalente: `parent_task_id` es `ON DELETE RESTRICT` y una tarea con
  sub-tareas no se puede borrar sin reasignarlas antes. Desenganchar una
  sub-tarea es `PATCH /tasks/{key}` con `parent: "none"`
  (`neugram task mv --parent none`).

## Tools MCP

Servidor `neugram mcp` (stdio). Cliente fino: traduce cada tool a una llamada
HTTP al daemon y devuelve su JSON. No abre la base y no tiene lógica propia.
Se registra con el nombre `neugram`, así que el agente ve las tools como
`mcp__neugram__<tool>`. Es el único MCP de memoria que ve el agente tras el
corte (ADR 0007), que ocurre en el paso 4; en el paso 3 corre en un ensayo
aislado y solo están las tools marcadas **3** en el índice.

Toda tool devuelve un único bloque de contenido `text` con el JSON del
envelope. Un error es un resultado con `isError: true` y el mismo objeto
`error` que devuelve el HTTP (ver [Errores](#errores)).

### Espejo de Engram

#### `mem_save`

**E** · Guarda una observación con su tarea. Es la escritura principal del
agente.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `title` | string | sí | — | Verbo + qué, corto y buscable |
| `content` | string | sí | — | El daemon agrega o corrige el pie `**Task**` (con pin `none`, ver Convenciones) |
| `type` | enum | sí | — | `bugfix` · `decision` · `architecture` · `discovery` · `pattern` · `config` · `preference` · `handoff` · `session_summary` |
| `task_id` | string **key** | no | pin de la sesión | `none` guarda sin tarea |
| `topic_key` | string | no | `null` | Upsert por `(topic_key, project, scope)`. Ver [`topic_key_collision`](#topic_key_collision) |
| `scope` | enum | no | `project` | `project` · `personal` |
| `project` | string | no | `sessions.project` de la sesión | Override explícito del repo de procedencia |
| `capture_prompt` | bool | no | — | Se acepta por compatibilidad con el CLAUDE.md y se ignora: el prompt lo captura el hook `user-prompt-submit` |
| *custom* | según `schema_fields` | según `required` | — | Columnas de `observations` |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "title": "API unificada en un solo documento",
  "content": "**What**: docs/specs/api.md absorbe specs/mcp-tools.md...\n\n**Task**: engram-fork-debat — Fork personal de Engram (debate de diseño)",
  "type": "architecture",
  "task_id": "engram-fork-debat",
  "impact": "high"
}
```

Respuesta:

```json
{
  "sync_id": "obs-9f2c1a7b4e5d6f80a1b2c3d4e5f60718",
  "type": "architecture",
  "title": "API unificada en un solo documento",
  "project": "neugram",
  "scope": "project",
  "topic_key": null,
  "kind": null,
  "task_id": "01JB7F3K9QZ4M2X8V6T1YH0RNC",
  "task_key": "engram-fork-debat",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "impact": "high",
  "created_at": "2026-09-21T14:32:10Z",
  "footer_rewritten": false,
  "deduped": false,
  "judgment_required": true,
  "candidates": [
    {
      "judgment_id": "rel-3c8a41d70b2e4f9a8c15d6e703b4a2f1",
      "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b",
      "title": "El MCP espeja los nombres de Engram",
      "type": "architecture",
      "project": "neugram",
      "created_at": "2026-09-14T11:02:44Z",
      "similarity": 0.83,
      "relation": "pending",
      "judgment_status": "pending",
      "preview": "**What**: el MCP de la capa usa mem_save, mem_search... **Where**: docs/adr/0008"
    }
  ]
}
```

`judgment_required` y `candidates` viajan **siempre**: los candidatos salen de
`FindCandidates` sobre las filas que se acaban de escribir, en la misma
transacción y en el mismo proceso; `similarity` y el umbral están en
`specs/data-model.md` (Candidatos de conflicto). `judgment_required` es `true` solo
si al menos un candidato tiene `judgment_status: pending`; es `false` con
`candidates: []` y también cuando todos los candidatos ya están juzgados. Cada candidato trae su propio
`judgment_id`: [`mem_judge`](#mem_judge) se llama una vez por candidato.
Cada candidato trae además `relation` y `judgment_status`: `pending` y `pending`
si está por juzgar, o la `relation` vigente y `judged` si el par ya se juzgó
(en cualquier dirección). Un par juzgado no se reabre: se devuelve con el
`judgment_id` existente y no hace falta volver a llamar a `mem_judge`. Las
reglas del par están en `specs/data-model.md` (Candidatos de conflicto).

`deduped: true` significa que `normalized_hash` encontró una observación igual
y se incrementó `duplicate_count` en vez de insertar una fila nueva; el
`sync_id` devuelto es el de la fila existente. `footer_rewritten: true`
significa que el pie `**Task**` del content no coincidía con `task_id` y se
corrigió.

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task) ·
[`invalid_type`](#invalid_type) · [`required_field`](#required_field) ·
[`invalid_enum`](#invalid_enum) · [`invalid_field`](#invalid_field) ·
[`topic_key_collision`](#topic_key_collision) ·
[`unknown_session`](#unknown_session)

#### `mem_update`

**E** · Modifica una observación ya guardada. Es lo correcto para arreglar un
registro conocido; un `mem_save` con el mismo `topic_key` lo pisaría en
silencio.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `sync_id` | string | sí | — | Clave pública de la observación |
| `title` | string | no | — | |
| `content` | string | no | — | |
| `type` | enum | no | — | Misma lista que `mem_save` |
| `topic_key` | string \| null | no | — | `null` explícito lo borra |
| `task_id` | string **key** | no | — | Cambiarlo reescribe el pie `**Task**` |
| `scope` | enum | no | — | |
| `kind` | enum \| null | no | — | `handoff` o `null` |
| *custom* | según `schema_fields` | no | — | |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

Sin ningún campo además de `sync_id`, la llamada falla con
[`invalid_request`](#invalid_request).

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "sync_id": "obs-9f2c1a7b4e5d6f80a1b2c3d4e5f60718", "task_id": "TRIPULSE-3652", "impact": "medium" }
```

```json
{
  "sync_id": "obs-9f2c1a7b4e5d6f80a1b2c3d4e5f60718",
  "title": "API unificada en un solo documento",
  "type": "architecture",
  "project": "neugram",
  "scope": "project",
  "topic_key": null,
  "kind": null,
  "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
  "task_key": "TRIPULSE-3652",
  "impact": "medium",
  "changed": ["task_id", "impact", "content"],
  "footer_rewritten": true,
  "revision_count": 2,
  "updated_at": "2026-09-21T15:10:03Z"
}
```

`content` aparece en `changed` sin haber sido enviado cuando el cambio de
`task_id` obligó a reescribir el pie.

Errores: [`not_found`](#not_found) · [`invalid_request`](#invalid_request) ·
[`unknown_task`](#unknown_task) · [`invalid_type`](#invalid_type) ·
[`invalid_enum`](#invalid_enum) · [`invalid_field`](#invalid_field) ·
[`topic_key_collision`](#topic_key_collision) ·
[`unknown_session`](#unknown_session)

#### `mem_search`

**E** · Búsqueda FTS5 sobre título y content, cruzada con los filtros de
columna. Con `task_id` trae la tarea entera cruzando repos, que es lo que
`mem_context` no puede hacer.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `query` | string | sí | — | Literal, sin operadores FTS5 (`specs/data-model.md`, Búsqueda de texto) |
| `match_mode` | enum | no | `all` | `all` · `any` |
| `task_id` | string **key** | no | — | No usa el pin: sin este parámetro no filtra por tarea |
| `include_ancestors` | bool | no | `true` | Con `task_id`: suma las observaciones de sus ancestros. Con tres niveles son a lo sumo dos ids extra |
| `include_subtasks` | bool | no | `false` | Con `task_id`: suma las de su subárbol entero, no sólo las hijas directas |
| `project` | string | no | — | Repo |
| `product` | string | no | — | Nombre de producto; filtra por las tareas de ese producto |
| `type` | string \| array | no | — | Uno o varios tipos |
| `kind` | enum \| null | no | — | `handoff` para buscar solo handoffs |
| `scope` | enum | no | — | `project` · `personal` |
| `since` / `until` | string | no | — | ISO 8601; sobre `created_at` |
| `where` | object | no | — | Columnas fijas o custom de `observations` |
| `full` | bool | no | `false` | `true` devuelve el content entero |
| `limit` | integer | no | `20` | Máx. 200 |
| `offset` | integer | no | `0` | |
| `session_id` | string | no | — | Opcional. Solo para el `project` por defecto. No activa el pin: el filtro por tarea sigue siendo `task_id` |

```json
{ "query": "parent_task_id jerarquía", "task_id": "engram-fork-debat", "full": false, "limit": 5 }
```

```json
{
  "query": "parent_task_id jerarquía",
  "total": 2,
  "limit": 5,
  "offset": 0,
  "truncated": false,
  "results": [
    {
      "sync_id": "obs-7d40c9b1582a4e6f93c0ad71be58f204",
      "title": "Jerarquía de tareas con profundidad 3 y triggers",
      "type": "decision",
      "project": "neugram",
      "scope": "project",
      "topic_key": null,
      "kind": null,
      "task_id": "01JB7F3K9QZ4M2X8V6T1YH0RNC",
      "task_key": "engram-fork-debat",
      "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
      "created_at": "2026-09-21T12:48:19Z",
      "score": 11.42,
      "preview": "**What**: tasks.parent_task_id con ON DELETE RESTRICT y dos triggers BEFORE que acotan el nivel del padre y la altura del subárbol. **Why**: SQLite no admite subqueries en CHECK..."
    }
  ]
}
```

`preview` son los primeros 300 caracteres del content; con `full: true` la
clave es `content` con el texto entero. `truncated: true` avisa que `total`
supera `limit + offset`. `score` es `-bm25`: más alto = mejor.

`task_key` en cada resultado dice de qué tarea es la observación, que con
`include_ancestors` o `include_subtasks` no es necesariamente la pedida. Los
dos flags se ignoran sin `task_id`.

Errores: [`invalid_field`](#invalid_field) · [`unknown_task`](#unknown_task) ·
[`invalid_request`](#invalid_request) (`limit` fuera de rango, `query` vacía)

#### `mem_context`

**E** · Contexto reciente sin buscar nada. Sin `task_id` se comporta como en
Engram: las últimas observaciones del repo de la sesión. Con `task_id` devuelve
además el último handoff de la tarea y sus worktrees vivos.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `task_id` | string **key** | no | — | Sin él, no usa el pin: devuelve el contexto del repo |
| `include_ancestors` | bool | no | `true` | Suma las observaciones de los ancestros de la tarea |
| `include_subtasks` | bool | no | `false` | Suma las de su subárbol entero |
| `project` | string | no | `sessions.project` de la sesión | Con `session_id` ausente no hay default: hay que pasarlo |
| `limit` | integer | no | `20` | Máx. 100 |
| `session_id` | string | no | — | Opcional. Solo para el `project` por defecto. No activa el pin: el filtro por tarea sigue siendo `task_id` |

```json
{ "task_id": "TRIPULSE-3652-1", "limit": 3 }
```

```json
{
  "scope_used": "task",
  "project": "neugram",
  "task": {
    "task_id": "01JB7G0RQ5D8W3N6K2P4ZXAMHT",
    "task_key": "TRIPULSE-3652-1",
    "title": "Jerarquía de tareas",
    "status": "in-progress",
    "product": "Microservices",
    "parent_key": "TRIPULSE-3652",
    "ancestor_keys": ["TRIPULSE-3652"],
    "children_keys": ["TRIPULSE-3652-1-a"]
  },
  "observations": [
    {
      "sync_id": "obs-7d40c9b1582a4e6f93c0ad71be58f204",
      "title": "Jerarquía de tareas con profundidad 3 y triggers",
      "type": "decision",
      "project": "neugram",
      "task_key": "TRIPULSE-3652-1",
      "created_at": "2026-09-21T12:48:19Z",
      "preview": "**What**: tasks.parent_task_id con ON DELETE RESTRICT y dos triggers BEFORE..."
    },
    {
      "sync_id": "obs-5f19b0c7a3d24e8b91072ce4d6a3f8b1",
      "title": "Alcance de Best Skills acotado a tres entregables",
      "type": "decision",
      "project": "neugram",
      "task_key": "TRIPULSE-3652",
      "created_at": "2026-09-19T10:02:44Z",
      "preview": "**What**: el épico cierra con el doc de API, la jerarquía y el import..."
    }
  ],
  "last_handoff": {
    "sync_id": "obs-2a7f83c15d0b4e9681c3f70a4b6d29e5",
    "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
    "created_at": "2026-09-20T22:14:07Z",
    "content": "## Goal\nDefinir la API...\n\n**Task**: TRIPULSE-3652 — Best Skills"
  },
  "worktrees": [
    {
      "id": "01JB6X1M4A7P2K9D0Z3V8QYRTB",
      "path": "/home/rcrespodev/rcrespodev/neugram-api",
      "repo": "neugram",
      "branch": "feat/api-doc",
      "alive": true
    }
  ]
}
```

`scope_used` es `task` o `project`, para que el agente sepa qué recorte
recibió. Sin `task_id` las claves `task`, `last_handoff` y `worktrees` no
aparecen. Las observaciones del ejemplo son dos porque `include_ancestors`
está prendido por default y la segunda es del épico: cada una dice de quién es
en su `task_key`. `children_keys` son las hijas directas, y están para que el
agente sepa que existen y pueda pedirlas por su `key`; sus observaciones no
vienen salvo con `include_subtasks`. `last_handoff` y `worktrees` son siempre
de la tarea pedida, nunca de un ancestro.

Errores: [`unknown_task`](#unknown_task) · [`invalid_request`](#invalid_request)

#### `mem_get_observation`

**E** · Una observación entera, sin recorte de preview.

| Parámetro | Tipo | Oblig. | Default |
|---|---|---|---|
| `sync_id` | string | sí | — |

```json
{
  "sync_id": "obs-7d40c9b1582a4e6f93c0af71be58f204",
  "title": "Jerarquía de tareas con profundidad 3 y triggers",
  "content": "**What**: tasks.parent_task_id...\n\n**Task**: engram-fork-debat — Fork personal de Engram (debate de diseño)",
  "type": "decision",
  "project": "neugram",
  "scope": "project",
  "topic_key": null,
  "kind": null,
  "tool_name": null,
  "task_id": "01JB7F3K9QZ4M2X8V6T1YH0RNC",
  "task_key": "engram-fork-debat",
  "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
  "impact": "high",
  "revision_count": 1,
  "duplicate_count": 1,
  "last_seen_at": "2026-09-21T12:48:19Z",
  "review_after": "2026-12-20T12:48:19Z",
  "needs_review": false,
  "pinned": false,
  "created_at": "2026-09-21T12:48:19Z",
  "updated_at": "2026-09-21T12:48:19Z"
}
```

`needs_review` es derivado: `review_after` ya pasó.

Errores: [`not_found`](#not_found)

#### `mem_save_prompt`

**E** · Guarda el prompt del usuario. Existe por compatibilidad con el
CLAUDE.md: en neugram lo hace el hook `user-prompt-submit`, así que la tool
deduplica contra lo que el hook ya escribió en la misma sesión.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `content` | string | sí | — | Verbatim, no parafraseado |
| `project` | string | no | `sessions.project` de la sesión | Con `session_id` ausente no hay default: hay que pasarlo |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "sync_id": "prm-5c81a03fd7924e6b8105fa32c94d7e60",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "project": "neugram",
  "created_at": "2026-09-21T14:29:55Z",
  "deduped": true
}
```

`deduped: true` significa que el hook ya lo había guardado y se devuelve ese
`sync_id` sin insertar nada.

Errores: [`unknown_session`](#unknown_session)

#### `mem_judge`

**E** · Resuelve un candidato de conflicto devuelto por `mem_save`. Una
llamada por candidato, con el `judgment_id` de ese candidato.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `judgment_id` | string | sí | — | El `sync_id` de la relación (`rel-…`) |
| `relation` | enum | sí | — | `supersedes` · `conflicts_with` · `related` · `compatible` · `scoped` · `not_conflict` |
| `reason` | string | no | — | Por qué |
| `evidence` | string | no | — | Qué lo respalda |
| `confidence` | number | no | — | 0–1 |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "judgment_id": "rel-3c8a41d70b2e4f9a8c15d6e703b4a2f1",
  "relation": "supersedes",
  "reason": "La absorción de Engram reemplaza el diseño de capa del ADR 0001",
  "confidence": 0.92
}
```

```json
{
  "judgment_id": "rel-3c8a41d70b2e4f9a8c15d6e703b4a2f1",
  "source_sync_id": "obs-9f2c1a7b4e5d6f80a1b2c3d4e5f60718",
  "target_sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b",
  "relation": "supersedes",
  "judgment_status": "judged",
  "confidence": 0.92,
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "superseded_relation_id": null,
  "updated_at": "2026-09-21T14:33:02Z"
}
```

Volver a juzgar una relación ya juzgada no falla: marca la anterior con
`superseded_at` y devuelve la nueva, con `superseded_relation_id` apuntando a
la que reemplazó.

Juzgar una relación `superseded` se rechaza con
[`invalid_request`](#invalid_request) (ningún código del catálogo expresa
"estado incompatible", y es el más cercano a un `judgment_id` que no se puede
usar): el `message` nombra el `judgment_id` de la cabeza de la cadena, que es
el que se juzga.

Errores: [`not_found`](#not_found) · [`invalid_enum`](#invalid_enum) ·
[`invalid_request`](#invalid_request) · [`unknown_session`](#unknown_session)

#### `mem_session_summary`

**E** · Resumen de cierre como observación `type=session_summary`. El cierre
normal de una sesión pineada es [`handoff_save`](#handoff_save); esta tool es
el fallback para sesiones `none` o cuando no se corrió `/handoff`.

| Parámetro | Tipo | Oblig. | Default |
|---|---|---|---|
| `content` | string | sí | — |
| `task_id` | string **key** | no | pin de la sesión |
| `title` | string | no | `Session summary <session_id corto>` |
| `session_id` | string | sí | — |

```json
{
  "sync_id": "obs-e04b917c6a2f4d3890be15c7a2f80d63",
  "type": "session_summary",
  "title": "Session summary e8d02241",
  "task_id": null,
  "task_key": "none",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "created_at": "2026-09-21T18:02:41Z"
}
```

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task) ·
[`unknown_session`](#unknown_session)

#### `mem_timeline`

**E** · Observaciones vecinas de la misma sesión, para reconstruir el orden en
que pasaron las cosas.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `sync_id` | string | sí | — | La observación ancla |
| `before` | integer | no | `3` | Máx. 20 |
| `after` | integer | no | `3` | Máx. 20 |

```json
{
  "anchor": "obs-7d40c9b1582a4e6f93c0af71be58f204",
  "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
  "before": [
    {
      "sync_id": "obs-1f83a5d0c94b7e26a8df03b51c7e9042",
      "title": "Absorción de Engram: una sola base",
      "type": "architecture",
      "created_at": "2026-09-21T11:57:03Z"
    }
  ],
  "after": []
}
```

Errores: [`not_found`](#not_found) · [`invalid_request`](#invalid_request)

#### `mem_review`

**E** · Observaciones cuyo `review_after` venció: contexto viejo que hay que
verificar contra la evidencia actual antes de usarlo.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `action` | enum | sí | — | `list` · `mark_reviewed` |
| `project` | string | no | `sessions.project` de la sesión | Solo `list` |
| `type` | string | no | — | Solo `list` |
| `limit` | integer | no | `20` | Solo `list`; máx. 100 |
| `sync_id` | string | sí con `mark_reviewed` | — | |
| `next_review_days` | integer | no | `90` | Solo `mark_reviewed` |
| `session_id` | string | sí con `mark_reviewed` | — | Con `list` es opcional y solo fija el `project` por defecto |

`list`:

```json
{
  "action": "list",
  "project": "neugram",
  "total": 1,
  "results": [
    {
      "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b",
      "title": "El MCP espeja los nombres de Engram",
      "type": "architecture",
      "project": "neugram",
      "task_key": "engram-fork-debat",
      "review_after": "2026-09-14T11:02:44Z",
      "days_overdue": 7,
      "preview": "**What**: el MCP de la capa usa mem_save, mem_search..."
    }
  ]
}
```

`mark_reviewed`:

```json
{ "action": "mark_reviewed", "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b", "review_after": "2026-12-20T15:41:08Z" }
```

Errores: [`not_found`](#not_found) · [`invalid_enum`](#invalid_enum)

#### `mem_current_project`

**E** · Qué repo ve neugram desde el directorio de la sesión (`sessions.directory`), con qué producto y qué tarea está
pineada la sesión. Es la primera llamada recomendada de una sesión.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `session_id` | string | no | — | Opcional. Sin él la respuesta omite `pin`, `session_id` y `worktree`, igual que [`GET /project/current`](#get-projectcurrent) |

```json
{
  "project": "neugram",
  "directory": "/home/rcrespodev/rcrespodev/neugram",
  "detected_by": "git_remote",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "pin": {
    "task_id": "01JB7F3K9QZ4M2X8V6T1YH0RNC",
    "task_key": "engram-fork-debat",
    "title": "Fork personal de Engram (debate de diseño)",
    "status": "in-progress",
    "parent_key": null,
    "ancestor_keys": [],
    "product": "engram-fork",
    "pinned_at": "2026-09-21T11:04:12Z",
    "pinned_by": "agent"
  },
  "observations_count": 118,
  "worktree": {
    "registered": true,
    "path": "/home/rcrespodev/rcrespodev/neugram",
    "branch": "main"
  }
}
```

`detected_by` es `git_remote`, `git_root`, `basename` o `config_override`
(`.engram/config.json`), la misma escalera que escribió los 6.805 registros
importados. Sin pin, `pin` es `null`.

Errores: ninguno propio.

### Tools de flujo (propias)

#### `task_pin`

**N** · Fija la tarea de la sesión. Es lo primero que hace el agente en un
turno que va a mutar archivos: el gate `require-task-pin-on-mutation` lo exige.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `task_id` | string **key** | sí | — | `none` es un pin válido y explícito |
| `title` | string | no | — | Crea la tarea si la `key` no existe |
| `product` | string | no | el del padre | Nombre del producto; solo al crear. Con `parent` y sin `product`, copia el del padre en el momento de crear y no vuelve a seguirlo |
| `parent` | string **key** | no | — | Crea la tarea colgando de esa `key`, que tiene que estar en el nivel 1 o 2 |
| `tracker` | string | no | — | `clickup:<id>:<url>` |
| `notes` | string | no | — | |
| *custom* | según `schema_fields` | según `required` | — | Columnas de `tasks` |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

Con una `key` inexistente y sin `title`, falla con
[`unknown_task`](#unknown_task) y el `hint` lista las tareas abiertas. Repetir
con otra tarea repinea y lo deja en el log. `pinned_by` es siempre `agent`
desde el MCP.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "task_id": "TRIPULSE-3652-1", "title": "Jerarquía de tareas", "parent": "TRIPULSE-3652", "product": "Microservices" }
```

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "task_id": "01JB7G0RQ5D8W3N6K2P4ZXAMHT",
  "task_key": "TRIPULSE-3652-1",
  "title": "Jerarquía de tareas",
  "status": "todo",
  "product": "Microservices",
  "parent_key": "TRIPULSE-3652",
  "ancestor_keys": ["TRIPULSE-3652"],
  "created": true,
  "repinned_from": "engram-fork-debat",
  "pinned_at": "2026-09-21T15:22:40Z",
  "pinned_by": "agent"
}
```

Un pin a `none` devuelve `task_id: null`, `task_key: "none"`, `created: false`
y `pinned_at` con valor: eso es lo que distingue "pin explícito a ninguna" de
"sin pin".

Errores: [`unknown_task`](#unknown_task) · [`task_exists`](#task_exists) ·
[`unknown_product`](#unknown_product) · [`invalid_parent`](#invalid_parent) ·
[`required_field`](#required_field) · [`invalid_enum`](#invalid_enum) ·
[`unknown_session`](#unknown_session)

#### `task_list`

**N** · Tareas con lo que el agente necesita para elegir una: producto,
status, actividad y worktrees vivos.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `status` | string \| array | no | — | Uno o varios de los cinco |
| `product` | string | no | — | |
| `parent` | string **key** | no | — | Solo las hijas directas de esa tarea, no el subárbol |
| `roots_only` | bool | no | `false` | Solo épicos (nivel 1) |
| `include_done` | bool | no | `false` | |
| `tree` | bool | no | `false` | Anida cada tarea dentro de su padre en `children`, hasta los tres niveles |
| `where` | object | no | — | Columnas fijas o custom de `tasks` |
| `limit` | integer | no | `50` | Máx. 200 |
| `session_id` | string | no | — | Opcional. Solo para resolver el pin y el `project` por defecto; sin él no hay pin implícito |

```json
{ "product": "Microservices", "tree": true }
```

```json
{
  "total": 2,
  "tree": true,
  "tasks": [
    {
      "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
      "task_key": "TRIPULSE-3652",
      "title": "Best Skills",
      "status": "in-progress",
      "product": "Microservices",
      "parent_key": null,
      "ancestor_keys": [],
      "tracker": { "kind": "clickup", "id": "86dxyz123", "url": "https://app.clickup.com/t/86dxyz123" },
      "sessions_count": 14,
      "worktrees_alive": 1,
      "observations_count": 87,
      "last_observation_at": "2026-09-21T12:48:19Z",
      "children": [
        {
          "task_id": "01JB7G0RQ5D8W3N6K2P4ZXAMHT",
          "task_key": "TRIPULSE-3652-1",
          "title": "Jerarquía de tareas",
          "status": "in-progress",
          "product": "Microservices",
          "parent_key": "TRIPULSE-3652",
          "ancestor_keys": ["TRIPULSE-3652"],
          "sessions_count": 1,
          "worktrees_alive": 0,
          "observations_count": 3,
          "last_observation_at": "2026-09-21T15:31:02Z",
          "children": [
            {
              "task_id": "01JB7K8T3P6Q2V5X9B1MZWCDNE",
              "task_key": "TRIPULSE-3652-1-a",
              "title": "Triggers de profundidad",
              "status": "todo",
              "product": "Microservices",
              "parent_key": "TRIPULSE-3652-1",
              "ancestor_keys": ["TRIPULSE-3652", "TRIPULSE-3652-1"],
              "sessions_count": 1,
              "worktrees_alive": 0,
              "observations_count": 1,
              "last_observation_at": "2026-09-22T09:12:40Z",
              "children": []
            }
          ]
        }
      ]
    }
  ]
}
```

Con `tree: false` (default) la lista es plana y cada fila trae su `parent_key`
y su `ancestor_keys`; `children` no aparece. Con `tree: true` el anidado llega
hasta el nivel 3 y `children` es `[]` en las hojas. Los contadores son siempre
de la tarea en sí, nunca la suma de su subárbol.

Errores: [`invalid_field`](#invalid_field) · [`unknown_task`](#unknown_task)
(`parent` inexistente) · [`invalid_enum`](#invalid_enum)

#### `task_status`

**N** · Cambia el status de una tarea. Lo declara el usuario a través del
agente; ningún hook llama esta tool.

| Parámetro | Tipo | Oblig. | Default |
|---|---|---|---|
| `task_id` | string **key** | no | pin de la sesión |
| `status` | enum | sí | — |
| `session_id` | string | sí | — |

`session_id` es obligatorio aunque la tool cambie una tarea y no el pin: queda como autora del cambio en el log.

`status` ∈ `todo` · `in-progress` · `review` · `blocked` · `done`.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "task_id": "TRIPULSE-3652-1", "status": "in-progress" }
```

```json
{
  "task_id": "01JB7G0RQ5D8W3N6K2P4ZXAMHT",
  "task_key": "TRIPULSE-3652-1",
  "status": "in-progress",
  "previous_status": "todo",
  "parent_key": "TRIPULSE-3652",
  "ancestor_keys": ["TRIPULSE-3652"],
  "open_children": 0,
  "updated_at": "2026-09-21T15:34:18Z"
}
```

`open_children` cuenta las hijas directas con `status != done`, no el subárbol:
un épico con todas sus tareas en `done` y una sub-tarea abierta reporta `0`.
Pasar una tarea a `done` con `open_children > 0` **no falla**: el cambio se
aplica y el número viaja en la respuesta como aviso. Nadie más que el usuario
decide cuándo una tarea está cerrada.

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task) ·
[`invalid_enum`](#invalid_enum) ·
[`unknown_session`](#unknown_session)

#### `task_show`

**N** · Ficha completa: identidad, jerarquía, sesiones, worktrees con
liveness, handoffs y reportes de progreso.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `task_id` | string **key** | no | pin de la sesión | |
| `include_subtasks` | bool | no | `true` | Agrega `children` y las observaciones de todo el subárbol, no sólo de las hijas directas |
| `observations_limit` | integer | no | `10` | Máx. 100; `0` las omite |
| `session_id` | string | no | — | Opcional. Solo para resolver el pin y el `project` por defecto; sin él no hay pin implícito |

```json
{
  "task": {
    "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
    "task_key": "TRIPULSE-3652",
    "title": "Best Skills",
    "status": "in-progress",
    "product": "Microservices",
    "parent_key": null,
    "ancestor_keys": [],
    "tracker": { "kind": "clickup", "id": "86dxyz123", "url": "https://app.clickup.com/t/86dxyz123" },
    "notes": null,
    "impact": "high",
    "created_at": "2026-08-11T09:15:00Z",
    "updated_at": "2026-09-21T15:34:18Z"
  },
  "children": [
    {
      "task_key": "TRIPULSE-3652-1",
      "title": "Jerarquía de tareas",
      "status": "in-progress",
      "observations_count": 3,
      "children": [
        { "task_key": "TRIPULSE-3652-1-a", "title": "Triggers de profundidad", "status": "todo", "observations_count": 1, "children": [] }
      ]
    }
  ],
  "sessions": [
    {
      "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
      "project": "neugram",
      "directory": "/home/rcrespodev/rcrespodev/neugram",
      "started_at": "2026-09-20T18:40:11Z",
      "ended_at": "2026-09-20T22:15:02Z",
      "pinned_by": "agent"
    }
  ],
  "worktrees": [
    {
      "id": "01JB6X1M4A7P2K9D0Z3V8QYRTB",
      "path": "/home/rcrespodev/rcrespodev/neugram-api",
      "repo": "neugram",
      "branch": "feat/api-doc",
      "base": "main",
      "purpose": "documento único de API",
      "alive": true,
      "created_by_session": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041"
    }
  ],
  "handoffs": [
    {
      "sync_id": "obs-2a7f83c15d0b4e9681c3f70a4b6d29e5",
      "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
      "title": "Handoff TRIPULSE-3652: API definida en un solo doc",
      "created_at": "2026-09-20T22:14:07Z"
    }
  ],
  "progress_reports": [
    {
      "id": "01JB7H2C8E1N5R7T3W9YKFVXQ0",
      "delegation_id": "api-04",
      "role": "impl",
      "status": "done",
      "title": "Documento único de la API",
      "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
      "created_at": "2026-09-21T15:55:30Z"
    }
  ],
  "observations": [
    {
      "sync_id": "obs-7d40c9b1582a4e6f93c0af71be58f204",
      "title": "Jerarquía de tareas con profundidad 3 y triggers",
      "type": "decision",
      "project": "neugram",
      "created_at": "2026-09-21T12:48:19Z",
      "preview": "**What**: tasks.parent_task_id con ON DELETE RESTRICT..."
    }
  ]
}
```

`sessions`, `worktrees`, `handoffs` y `observations` son de la tarea pedida.
Con `include_subtasks: true` (default) entran también las observaciones de su
subárbol entero —hijas y nietas—, cada una con su `task_key`; las sesiones y
los worktrees no se heredan en ninguna dirección. `children` son las hijas
directas, cada una con sus propias `children`.

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task)

#### `worktree_register`

**N** · Registra un `git worktree` recién creado. Es lo que exige el gate
`require-worktree-registered`: el turno que corrió `git worktree add` no cierra
sin esto.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `path` | string | sí | — | Absoluto; es la clave del upsert |
| `repo` | string | sí | — | |
| `branch` | string | sí | — | |
| `base` | string | no | — | Rama de la que salió |
| `purpose` | string | no | — | Una línea |
| `task_id` | string **key** | no | pin de la sesión | |
| *custom* | según `schema_fields` | según `required` | — | Columnas de `worktrees` |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "path": "/home/rcrespodev/rcrespodev/neugram-api",
  "repo": "neugram",
  "branch": "feat/api-doc",
  "base": "main",
  "purpose": "documento único de API"
}
```

```json
{
  "id": "01JB6X1M4A7P2K9D0Z3V8QYRTB",
  "path": "/home/rcrespodev/rcrespodev/neugram-api",
  "repo": "neugram",
  "branch": "feat/api-doc",
  "base": "main",
  "purpose": "documento único de API",
  "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
  "task_key": "TRIPULSE-3652",
  "created_by_session": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "alive": true,
  "created": true,
  "created_at": "2026-09-21T15:41:55Z"
}
```

`created: false` significa que el `path` ya estaba registrado y se
actualizaron los demás campos. `alive` lo decide el filesystem en el momento
de responder; no se guarda.

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task) ·
[`invalid_request`](#invalid_request) (`path` relativo) ·
[`required_field`](#required_field) ·
[`unknown_session`](#unknown_session)

#### `handoff_save`

**N** · Cierre de sesión. Es un `mem_save` con `type=handoff`, `kind=handoff` y
uno por sesión: repetirlo actualiza el mismo registro. Es lo que
`session-start` le entrega a la sesión siguiente. Cuerpo de
[`PUT /sessions/{id}/handoff`](#put-sessionsidhandoff).

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `content` | string | sí | — | Markdown; el daemon agrega el pie `**Task**` |
| `task_id` | string **key** | no | pin de la sesión | |
| `title` | string | no | `Handoff <key>: <primera línea del State>` | |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "sync_id": "obs-2a7f83c15d0b4e9681c3f70a4b6d29e5",
  "type": "handoff",
  "kind": "handoff",
  "title": "Handoff TRIPULSE-3652: API definida en un solo doc",
  "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
  "task_key": "TRIPULSE-3652",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "project": "neugram",
  "replaced": false,
  "created_at": "2026-09-21T17:58:22Z"
}
```

`replaced: true` cuando la sesión ya tenía handoff y este lo pisó.

Errores: [`no_pin`](#no_pin) · [`unknown_task`](#unknown_task) ·
[`unknown_session`](#unknown_session)

#### `progress_write`

**N** · Lo que un subagente escribe al terminar su delegación, en vez de
volcarle todo al padre. Es lo que exige el gate `require-progress-report` en
`subagent-stop`.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `delegation_id` | string | sí | — | Lo asigna el padre (`api-04`, `explore-auth`). No es `tasks.key` |
| `role` | enum | sí | — | `explore` · `impl` · `review` |
| `status` | enum | sí | — | `done` · `blocked` |
| `title` | string | sí | — | |
| `summary` | array[string] | sí | — | 5 a 10 |
| `next_steps` | array[string] | sí | — | 3 a 6 |
| `refs` | object | no | — | `{engram_topics, files, links}`; `files` como `path:line` |
| `session_id` | string | sí | — | Obligatorio y explícito: el valor que imprime `session-start`. Sin fallback al entorno |

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "delegation_id": "api-04",
  "role": "impl",
  "status": "done",
  "title": "Documento único de la API",
  "summary": ["19 tools MCP y 42 rutas HTTP documentadas", "specs/mcp-tools.md absorbido y eliminado"],
  "next_steps": ["Revisar el catálogo de errores contra los gates"],
  "refs": { "files": ["docs/specs/api.md:1"], "engram_topics": [], "links": [] }
}
```

```json
{
  "id": "01JB7H2C8E1N5R7T3W9YKFVXQ0",
  "delegation_id": "api-04",
  "status": "done",
  "role": "impl",
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD",
  "task_key": "TRIPULSE-3652",
  "worktree_id": null,
  "replaced": false,
  "created_at": "2026-09-21T15:55:30Z"
}
```

La respuesta es corta a propósito: `id`, `delegation_id` y `status` es todo lo
que el subagente le pasa al padre. Upsert por `(session_id, delegation_id)`;
`replaced: true` cuando ya existía. `task_id` se fija al escribir y no sigue un
repin posterior. `worktree_id` es el worktree registrado que contiene el cwd,
o `null`.

Errores: [`invalid_report`](#invalid_report) · [`invalid_enum`](#invalid_enum) ·
[`unknown_session`](#unknown_session)

#### `progress_list`

**N** · Lee reportes de progreso enteros. Sin filtros, los de la sesión
actual; con `task_id`, los de todas sus sesiones.

| Parámetro | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `delegation_id` | string | no | — | |
| `task_id` | string **key** | no | — | Sin él y sin `session_id`, la sesión actual |
| `session_id` | string | no | — | |
| `role` | enum | no | — | |
| `status` | enum | no | — | |
| `limit` | integer | no | `20` | Máx. 100 |

```json
{
  "total": 1,
  "scope_used": "session",
  "reports": [
    {
      "id": "01JB7H2C8E1N5R7T3W9YKFVXQ0",
      "delegation_id": "api-04",
      "role": "impl",
      "status": "done",
      "title": "Documento único de la API",
      "summary": ["19 tools MCP y 42 rutas HTTP documentadas", "specs/mcp-tools.md absorbido y eliminado"],
      "next_steps": ["Revisar el catálogo de errores contra los gates"],
      "refs": { "files": ["docs/specs/api.md:1"], "engram_topics": [], "links": [] },
      "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
      "task_key": "TRIPULSE-3652",
      "worktree_id": null,
      "created_at": "2026-09-21T15:55:30Z",
      "updated_at": "2026-09-21T15:55:30Z"
    }
  ]
}
```

`scope_used` es `session`, `task` o `delegation`.

Errores: [`unknown_task`](#unknown_task) · [`invalid_enum`](#invalid_enum)

### Tools de Engram que no se espejan

| Tool | Por qué no |
|---|---|
| `mem_delete` | Borrar memoria es una acción del usuario, no del agente. La UI la ofrece por `DELETE /observations/{sync_id}` |
| `mem_pin` / `mem_unpin` | El pin de observación de Engram compite con el pin de sesión de neugram por el mismo nombre; se resuelve con `task_id`, y `observations.pinned` queda para la UI |
| `mem_session_start` / `mem_session_end` | Los cubren los hooks `session-start` y `session-end`: el agente no puede leer su propio `session_id` ni sabe cuándo termina |
| `mem_capture_passive` | La captura pasiva de tool calls no entra en la primera versión |
| `mem_merge_projects` | Operación destructiva sobre el eje repo; va por CLI |
| `mem_stats` | Lo cubren la UI y `neugram doctor` |
| `mem_doctor` | Es `neugram doctor` y `GET /doctor`: diagnóstico del daemon, no una tool |
| `mem_compare` | Comparar dos observaciones es lectura que el agente ya hace con `mem_get_observation` |
| `mem_suggest_topic_key` | `topic_key` es opcional y peligroso: sugerirlo empuja a usarlo |

Las de sesión las cubren los hooks; el resto es operación y va por CLI o UI.

### Lo que el MCP no expone

`schema add/rm`, `instructions edit`, `gate enable/disable`, `import-legacy` y
`product add` no son tools. Son CLI (ADR 0008): el agente no edita sus propias
reglas. Si el agente las pide, la respuesta es el comando que el usuario tiene
que correr.

Las rutas HTTP que las implementan sí existen —el CLI habla con el daemon,
no con la base— pero el cliente MCP nunca las llama. La frontera es el MCP, no
el HTTP: un puerto local sin autenticación no es una frontera de permisos
(ADR 0005). En la UI web esas pantallas son de solo lectura, con el comando
exacto al lado.

## HTTP del daemon

`neugram serve` escucha en `127.0.0.1:7438` y es el único proceso con conexión
a `~/.neugram/neugram.db`. Lo consumen el MCP, el CLI, los hooks y la UI web;
la UI se sirve del mismo puerto en `/` y sus rutas de página están en
`specs/web-ui.md`, no acá.

### Convenciones HTTP

- `Content-Type: application/json` en request y response. La UI es la única
  excepción: sus páginas son HTML y sus acciones htmx entran por estas mismas
  rutas con `Accept: text/html`, que devuelve el fragmento en vez del JSON.
- Sin autenticación: un usuario, loopback, sin login (ADR 0005).
- **`session_id` es obligatorio en el body de toda escritura.** El daemon no
  tiene entorno del que deducirlo. Una escritura con un `session_id` que no
  existe en `sessions` falla con [`unknown_session`](#unknown_session), sin
  crearla: la sesión la crea `POST /sessions` o `POST /hooks/session-start`.
- Los parámetros de lectura van en query string; `where` y otros objetos se
  mandan como JSON urlencodeado: `?where=%7B%22impact%22%3A%22high%22%7D`.
- Todo listado devuelve `{total, limit, offset, <colección>}`.
- Los códigos: `200` lectura y actualización, `201` creación, `204` sin
  cuerpo, `400` payload inválido, `404` no existe, `409` colisión,
  `422` regla de negocio (required, enum, jerarquía), `500` interno. El cuerpo
  de todo error ≥400 es el objeto de [Errores](#errores).

### Salud y proyecto

#### `GET /health`

**N** · Lo que el hook consulta para saber si tiene que levantar el daemon.
Espera hasta 2 s a que responda.

```json
{ "status": "ok", "version": "0.1.0", "db": "/home/rcrespodev/.neugram/neugram.db", "schema_version": 7, "uptime_s": 8412 }
```

Errores: ninguno. No responder es la respuesta negativa.

#### `POST /shutdown`

**N** · Cierra el daemon: termina las requests en curso, cierra la base y
libera el puerto. La llama el cliente cuando `/health.version` no coincide
con la suya (`specs/hooks.md`, Ciclo de vida del daemon). Body vacío.

```json
{ "status": "stopping" }
```

Errores: ninguno.

#### `GET /project/current`

**E** · Detección de repo desde un directorio. Es el cuerpo de
[`mem_current_project`](#mem_current_project).

| Query | Tipo | Oblig. | Default |
|---|---|---|---|
| `directory` | string | sí | — |
| `session_id` | string | no | — |

Sin `session_id` la respuesta omite `pin`, `session_id` y `worktree`. Con él,
devuelve el mismo objeto que [`mem_current_project`](#mem_current_project).

```json
{ "project": "neugram", "directory": "/home/rcrespodev/rcrespodev/neugram", "detected_by": "git_remote", "observations_count": 118 }
```

Errores: [`invalid_request`](#invalid_request) (`directory` ausente o relativo)

### Memoria

#### `POST /observations`

**E** · Crea una observación. Cuerpo del `mem_save`. `201`.

Payload: el de [`mem_save`](#mem_save) más `session_id` (obligatorio).

```json
{
  "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1",
  "title": "API unificada en un solo documento",
  "content": "**What**: docs/specs/api.md absorbe specs/mcp-tools.md...",
  "type": "architecture",
  "task_id": "engram-fork-debat",
  "impact": "high"
}
```

Response: idéntica a la de [`mem_save`](#mem_save), con
`judgment_required` y `candidates` siempre presentes. La inserción de la fila,
el índice FTS (por trigger) y `FindCandidates` ocurren en una sola transacción.

Errores: `422` [`invalid_type`](#invalid_type) ·
[`required_field`](#required_field) · [`invalid_enum`](#invalid_enum) ·
[`no_pin`](#no_pin) · `404` [`unknown_task`](#unknown_task) ·
`409` [`topic_key_collision`](#topic_key_collision)

#### `GET /observations`

**E** · Búsqueda y listado. Con `q` es FTS; sin `q` es listado por filtros
ordenado por `created_at DESC`. Es el cuerpo de
[`mem_search`](#mem_search) y de la página `/observations` de la UI.

Query: `q`, `match_mode`, `task_id`, `project`, `product`, `type`, `kind`,
`scope`, `since`, `until`, `where`, `full`, `limit`, `offset`, `session_id`
(filtro, no autor).

```json
{
  "query": "parent_task_id jerarquía",
  "total": 2,
  "limit": 20,
  "offset": 0,
  "truncated": false,
  "results": [
    {
      "sync_id": "obs-7d40c9b1582a4e6f93c0af71be58f204",
      "title": "Jerarquía de tareas con profundidad 3 y triggers",
      "type": "decision",
      "project": "neugram",
      "task_key": "engram-fork-debat",
      "created_at": "2026-09-21T12:48:19Z",
      "score": 11.42,
      "preview": "**What**: tasks.parent_task_id con ON DELETE RESTRICT..."
    }
  ]
}
```

Errores: `400` [`invalid_request`](#invalid_request) ·
`422` [`invalid_field`](#invalid_field) · `404` [`unknown_task`](#unknown_task)

#### `GET /observations/{sync_id}`

**E** · Una observación entera. Cuerpo de
[`mem_get_observation`](#mem_get_observation); misma respuesta.

```json
{ "sync_id": "obs-7d40c9b1582a4e6f93c0af71be58f204", "title": "Jerarquía de tareas con profundidad 3 y triggers", "content": "**What**: ...", "type": "decision", "project": "neugram", "task_key": "engram-fork-debat", "needs_review": false, "created_at": "2026-09-21T12:48:19Z" }
```

Errores: `404` [`not_found`](#not_found)

#### `PATCH /observations/{sync_id}`

**E** · Modifica campos de una observación. Cuerpo de
[`mem_update`](#mem_update) y de la edición desde la UI.

Payload: el de [`mem_update`](#mem_update) sin `sync_id` (va en la ruta), más
`session_id`.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "task_id": "TRIPULSE-3652", "impact": "medium" }
```

Response: la de [`mem_update`](#mem_update).

Errores: `404` [`not_found`](#not_found) · `400`
[`invalid_request`](#invalid_request) · `422` [`invalid_type`](#invalid_type) ·
[`invalid_enum`](#invalid_enum) · [`invalid_field`](#invalid_field) ·
`409` [`topic_key_collision`](#topic_key_collision)

#### `DELETE /observations/{sync_id}`

**E** · Soft delete: escribe `deleted_at` y la saca de toda lectura. No la
expone el MCP —borrar memoria es del usuario, no del agente— sino la UI.
`204` sin cuerpo.

| Query | Tipo | Oblig. | Default |
|---|---|---|---|
| `session_id` | string | sí | — |

Errores: `404` [`not_found`](#not_found)

#### `GET /observations/{sync_id}/timeline`

**E** · Vecinas de la misma sesión. Cuerpo de [`mem_timeline`](#mem_timeline).

Query: `before` (default `3`), `after` (default `3`).

```json
{ "anchor": "obs-7d40c9b1582a4e6f93c0af71be58f204", "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041", "before": [{ "sync_id": "obs-1f83a5d0c94b7e26a8df03b51c7e9042", "title": "Absorción de Engram: una sola base", "type": "architecture", "created_at": "2026-09-21T11:57:03Z" }], "after": [] }
```

Errores: `404` [`not_found`](#not_found) · `400` [`invalid_request`](#invalid_request)

#### `POST /prompts`

**E** · Guarda un prompt del usuario. Lo llama el hook
`user-prompt-submit`; también es el cuerpo de
[`mem_save_prompt`](#mem_save_prompt). `201`.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `session_id` | string | sí | — |
| `content` | string | sí | — |
| `project` | string | no | repo de la sesión |

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "content": "quiero definir en un único documento la API de Engram" }
```

```json
{ "sync_id": "prm-5c81a03fd7924e6b8105fa32c94d7e60", "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "project": "neugram", "created_at": "2026-09-21T14:29:55Z", "deduped": false }
```

Errores: `404` [`unknown_session`](#unknown_session) ·
`400` [`invalid_request`](#invalid_request)

#### `GET /prompts`

**E** · Prompts de una sesión, para la página `/sessions/{id}` de la UI.

Query: `session_id`, `project`, `q` (FTS sobre `prompts_fts`), `limit`
(default `50`), `offset`.

```json
{
  "total": 1,
  "limit": 50,
  "offset": 0,
  "prompts": [
    { "sync_id": "prm-5c81a03fd7924e6b8105fa32c94d7e60", "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "project": "neugram", "content": "quiero definir en un único documento la API de Engram", "created_at": "2026-09-21T14:29:55Z" }
  ]
}
```

Un prompt no tiene `task_id`: se atribuye por el pin de su sesión, que es un
JOIN en la misma base.

Errores: `404` [`unknown_session`](#unknown_session)

#### `GET /review`

**E** · Observaciones con `review_after` vencido. Cuerpo de
[`mem_review`](#mem_review) con `action=list`.

Query: `project`, `type`, `limit` (default `20`).

```json
{ "total": 1, "limit": 20, "results": [ { "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b", "title": "El MCP espeja los nombres de Engram", "type": "architecture", "project": "neugram", "task_key": "engram-fork-debat", "review_after": "2026-09-14T11:02:44Z", "days_overdue": 7, "preview": "**What**: el MCP de la capa usa mem_save..." } ] }
```

Errores: ninguno propio.

#### `POST /review/mark_reviewed`

**E** · Corre `review_after` hacia adelante. Cuerpo de
[`mem_review`](#mem_review) con `action=mark_reviewed`.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b", "next_review_days": 90 }
```

```json
{ "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b", "review_after": "2026-12-20T15:41:08Z" }
```

Errores: `404` [`not_found`](#not_found)

### Conflictos

`FindCandidates`, `SaveRelation` y `JudgeRelation` son código propio bajo
`internal/store/relations.go`. No hay servicio externo al que
preguntarle, así que el envelope de conflictos sale completo de
`POST /observations`; estas rutas existen para juzgar después y para la UI.

#### `GET /conflicts`

**E** · Relaciones por estado de juicio.

Query: `status` (`pending` · `judged` · `superseded`; default `pending`),
`project`, `sync_id` (relaciones de una observación), `limit` (default `20`).

```json
{
  "total": 1,
  "limit": 20,
  "relations": [
    {
      "judgment_id": "rel-3c8a41d70b2e4f9a8c15d6e703b4a2f1",
      "source": { "sync_id": "obs-9f2c1a7b4e5d6f80a1b2c3d4e5f60718", "title": "API unificada en un solo documento", "type": "architecture" },
      "target": { "sync_id": "obs-4b1e7d2a90c35f68b7a04e1c8d93f52b", "title": "El MCP espeja los nombres de Engram", "type": "architecture" },
      "relation": "pending",
      "judgment_status": "pending",
      "confidence": null,
      "similarity": 0.83,
      "created_at": "2026-09-21T14:32:10Z"
    }
  ]
}
```

Errores: `400` [`invalid_request`](#invalid_request)

#### `POST /conflicts/judge`

**E** · Resuelve una relación. Cuerpo de [`mem_judge`](#mem_judge).

Payload: el de [`mem_judge`](#mem_judge) más `session_id`. Response: la de
`mem_judge`.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "judgment_id": "rel-3c8a41d70b2e4f9a8c15d6e703b4a2f1", "relation": "supersedes", "confidence": 0.92 }
```

Errores: `404` [`not_found`](#not_found) · `422` [`invalid_enum`](#invalid_enum)

#### `POST /conflicts/scan`

**E** · Corre `FindCandidates` sobre un project entero y crea las relaciones
`pending` que falten. Es mantenimiento: `POST /observations` ya lo hace por
cada escritura. No lo expone el MCP.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `project` | string | sí | — |
| `since` | string | no | — |
| `dry_run` | bool | no | `false` |

```json
{ "project": "neugram", "scanned": 118, "candidates_found": 4, "relations_created": 3, "already_pending": 1, "dry_run": false, "took_ms": 812 }
```

El payload de esta ruta era deuda mientras Engram estaba del otro lado de un
borde HTTP; con el código adentro, es el que dice esta tabla (ADR 0011).

Errores: `400` [`invalid_request`](#invalid_request)

### Sesiones

#### `POST /sessions`

**E** · Crea o actualiza la fila de la sesión. Idempotente por `id`: en
`resume` y `compact` la fila ya existe y conserva su pin. `201` la primera vez,
`200` después.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `id` | string | sí | — |
| `project` | string | no | detectado desde `directory` |
| `directory` | string | sí | — |

```json
{ "id": "e8d02241-459e-4faf-9438-165a3062dbb1", "directory": "/home/rcrespodev/rcrespodev/neugram" }
```

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "project": "neugram", "directory": "/home/rcrespodev/rcrespodev/neugram", "started_at": "2026-09-21T11:03:58Z", "created": true, "pin": null }
```

Errores: `400` [`invalid_request`](#invalid_request)

#### `GET /sessions/{id}`

**E** · Sesión con su pin, sus prompts y sus observaciones. Cuerpo de la
página `/sessions/{id}`.

Query: `include` (`prompts,observations`; default los dos), `limit`.

```json
{
  "session_id": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041",
  "project": "neugram",
  "directory": "/home/rcrespodev/rcrespodev/neugram",
  "started_at": "2026-09-20T18:40:11Z",
  "ended_at": "2026-09-20T22:15:02Z",
  "pin": { "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD", "task_key": "TRIPULSE-3652", "pinned_at": "2026-09-20T18:41:30Z", "pinned_by": "agent" },
  "counts": { "observations": 6, "prompts": 11, "progress_reports": 2 },
  "handoff": { "sync_id": "obs-2a7f83c15d0b4e9681c3f70a4b6d29e5", "created_at": "2026-09-20T22:14:07Z" },
  "observations": [],
  "prompts": []
}
```

Errores: `404` [`unknown_session`](#unknown_session)

#### `POST /sessions/{id}/pin`

**N** · Fija el pin. Cuerpo de [`task_pin`](#task_pin) y de
`neugram task pin --session`.

| Campo | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `task_id` | string **key** | sí | — | `none` incluido |
| `title`, `product`, `parent`, `tracker`, `notes`, *custom* | | no | — | Crean la tarea si la `key` no existe |
| `pinned_by` | enum | no | `agent` | `agent` · `hook` · `cli` · `import` |

Response: la de [`task_pin`](#task_pin).

```json
{ "task_id": "TRIPULSE-3652-1", "title": "Jerarquía de tareas", "parent": "TRIPULSE-3652", "pinned_by": "cli" }
```

Errores: `404` [`unknown_task`](#unknown_task) ·
[`unknown_session`](#unknown_session) · `409` [`task_exists`](#task_exists) ·
`422` [`invalid_parent`](#invalid_parent) · [`required_field`](#required_field)

#### `POST /sessions/{id}/end`

**E** · Marca `ended_at`. Lo llama el hook `session-end`. `204` sin cuerpo.

```json
{ "reason": "clear" }
```

Errores: `404` [`unknown_session`](#unknown_session)

#### `PUT /sessions/{id}/handoff`

**N** · Guarda el handoff de la sesión: upsert por `(session, kind=handoff)`.
Cuerpo de [`handoff_save`](#handoff_save). `POST /observations` no sabe
resolver ese upsert, y por eso la ruta es propia. `201` la primera vez, `200`
cuando reemplaza.

| Campo | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `content` | string | sí | — | Markdown; el daemon agrega o corrige el pie `**Task**` |
| `task_id` | string **key** | no | pin de la sesión | |
| `title` | string | no | `Handoff <key>: <primera línea del State>` | |

El `{id}` es el `session_id`. Response: la de [`handoff_save`](#handoff_save),
con `replaced: true` cuando la sesión ya tenía handoff.

```json
{ "content": "## State\nAPI definida en un solo doc...", "task_id": "TRIPULSE-3652" }
```

Errores: `404` [`unknown_session`](#unknown_session) ·
[`unknown_task`](#unknown_task) · `422` [`no_pin`](#no_pin)

### Flujo

#### `GET /tasks`

**N** · Cuerpo de [`task_list`](#task_list), de `neugram task ls`, de
`neugram task tree <key>` (que es `?tree=true` acotado al subárbol de `<key>`)
y del tablero de la UI.

Query: `status`, `product`, `parent`, `roots_only`, `include_done`, `tree`,
`where`, `limit`, `offset`. Response: la de [`task_list`](#task_list).

```json
{ "total": 2, "limit": 50, "offset": 0, "tree": false, "tasks": [ { "task_id": "01JB6WQ2XN8H4T0J5C3F7YKPRD", "task_key": "TRIPULSE-3652", "title": "Best Skills", "status": "in-progress", "product": "Microservices", "parent_key": null, "ancestor_keys": [], "sessions_count": 14, "worktrees_alive": 1, "observations_count": 87, "children_count": 1, "last_observation_at": "2026-09-21T12:48:19Z" } ] }
```

Errores: `422` [`invalid_field`](#invalid_field) · `404` [`unknown_task`](#unknown_task)

#### `POST /tasks`

**N** · Crea una tarea sin pinearla. Cuerpo de `neugram task add`. `201`.

| Campo | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `key` | string | sí | — | Única sin distinguir mayúsculas |
| `title` | string | sí | — | |
| `product` | string | no | el del padre | |
| `parent` | string **key** | no | — | |
| `status` | enum | no | `todo` | |
| `tracker` | object | no | — | `{kind, id, url}` |
| `notes` | string | no | — | |
| *custom* | según `schema_fields` | según `required` | — | |

```json
{ "key": "TRIPULSE-3652-2", "title": "Catálogo de errores", "parent": "TRIPULSE-3652" }
```

```json
{ "task_id": "01JB7J5V2M9B4C6X8N0QPWTZKD", "task_key": "TRIPULSE-3652-2", "title": "Catálogo de errores", "status": "todo", "product": "Microservices", "parent_key": "TRIPULSE-3652", "ancestor_keys": ["TRIPULSE-3652"], "created_at": "2026-09-21T16:02:11Z" }
```

Errores: `409` [`task_exists`](#task_exists) ·
`422` [`invalid_parent`](#invalid_parent) · [`required_field`](#required_field) ·
[`invalid_enum`](#invalid_enum) · `404` [`unknown_product`](#unknown_product)

#### `GET /tasks/{key}`

**N** · Ficha completa. Cuerpo de [`task_show`](#task_show), de
`neugram task show` y de la página `/tasks/{key}`.

Query: `include_subtasks` (default `true`), `observations_limit` (default
`10`). Response: la de [`task_show`](#task_show).

Errores: `404` [`unknown_task`](#unknown_task)

#### `PATCH /tasks/{key}`

**N** · Edita título, producto, tracker, notas, campos custom y padre. Cuerpo
de `neugram task edit` y de `neugram task mv`.

| Campo | Tipo | Oblig. | Default | Notas |
|---|---|---|---|---|
| `session_id` | string | sí | — | |
| `title`, `product`, `notes`, `tracker`, *custom* | | no | — | |
| `parent` | string **key** \| `"none"` | no | — | Arrastra el subárbol; falla si lo desborda. `none` desengancha la tarea y la vuelve épico |

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "parent": "none" }
```

```json
{ "task_id": "01JB7J5V2M9B4C6X8N0QPWTZKD", "task_key": "TRIPULSE-3652-2", "parent_key": null, "ancestor_keys": [], "changed": ["parent_task_id"], "updated_at": "2026-09-21T16:08:44Z" }
```

El status no se cambia por acá: tiene su propia ruta.

Errores: `404` [`unknown_task`](#unknown_task) ·
[`unknown_product`](#unknown_product) · `422` [`invalid_parent`](#invalid_parent) ·
[`invalid_enum`](#invalid_enum) · `400` [`invalid_request`](#invalid_request)

#### `PUT /tasks/{key}/status`

**N** · Cambia el status. Cuerpo de [`task_status`](#task_status) y de
`neugram task status`. Ningún hook la llama.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "status": "in-progress" }
```

Response: la de [`task_status`](#task_status).

Errores: `404` [`unknown_task`](#unknown_task) · `422` [`invalid_enum`](#invalid_enum)

#### `GET /products`

**N** · Productos con cuántas tareas cuelgan de cada uno.

Query: `q`, `limit`.

```json
{ "total": 2, "products": [ { "id": "01JB2Q7C5X0N9K3M8V4TZDRWFA", "name": "Microservices", "description": null, "tasks_count": 41, "open_tasks_count": 6 }, { "id": "01JB2Q7C5X0N9K3M8V4TZDRWFB", "name": "engram-fork", "description": "neugram y el debate de diseño", "tasks_count": 3, "open_tasks_count": 2 } ] }
```

Errores: ninguno propio.

#### `POST /products`

**N** · Crea un producto. Cuerpo de `neugram product add`. `201`.

```json
{ "name": "Portal", "description": "Portal de clientes" }
```

```json
{ "id": "01JB7K8N1R4D6F9H2J5LTWXCQV", "name": "Portal", "description": "Portal de clientes", "created_at": "2026-09-21T16:14:02Z" }
```

Errores: `409` [`product_exists`](#product_exists) ·
`400` [`invalid_request`](#invalid_request)

#### `GET /worktrees`

**N** · Worktrees con liveness calculada contra el filesystem al responder.

Query: `task_id`, `repo`, `alive` (`true` filtra los que existen en disco),
`where`, `limit`.

```json
{ "total": 1, "worktrees": [ { "id": "01JB6X1M4A7P2K9D0Z3V8QYRTB", "path": "/home/rcrespodev/rcrespodev/neugram-api", "repo": "neugram", "branch": "feat/api-doc", "base": "main", "purpose": "documento único de API", "task_key": "TRIPULSE-3652", "created_by_session": "b1c9f0e2-77a4-4c31-9f10-2d5e8a3b6041", "alive": true, "created_at": "2026-09-21T15:41:55Z" } ] }
```

Errores: `404` [`unknown_task`](#unknown_task) · `422` [`invalid_field`](#invalid_field)

#### `POST /worktrees`

**N** · Upsert por `path`. Cuerpo de
[`worktree_register`](#worktree_register). `201` si es nuevo, `200` si
actualizó.

Payload: el de [`worktree_register`](#worktree_register) más `session_id`.
Response: la de la tool.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "path": "/home/rcrespodev/rcrespodev/neugram-api", "repo": "neugram", "branch": "feat/api-doc", "base": "main" }
```

Errores: `400` [`invalid_request`](#invalid_request) ·
`404` [`unknown_task`](#unknown_task) · `422` [`required_field`](#required_field)

#### `GET /progress`

**N** · Reportes de progreso enteros. Cuerpo de
[`progress_list`](#progress_list) y de la ficha de tarea en la UI.

Query: `delegation_id`, `task_id`, `session_id`, `role`, `status`, `limit`,
`offset`. Response: la de [`progress_list`](#progress_list).

Errores: `404` [`unknown_task`](#unknown_task) · `422` [`invalid_enum`](#invalid_enum)

#### `POST /progress`

**N** · Upsert por `(session_id, delegation_id)`. Cuerpo de
[`progress_write`](#progress_write). `201` si es nuevo, `200` si reemplazó.

Payload: el de [`progress_write`](#progress_write) más `session_id`.
Response: la de la tool.

Errores: `422` [`invalid_report`](#invalid_report) ·
[`invalid_enum`](#invalid_enum) · `404` [`unknown_session`](#unknown_session)

### Operación

Estas rutas existen porque el CLI habla con el daemon, no con la base. **El
MCP no las llama**: el agente no cambia schema, instrucciones ni gates
(ADR 0008), y la UI las muestra en solo lectura con el comando al lado.

#### `GET /schema/fields`

**N** · Campos custom definidos. Lo lee el CLI, la UI y el MCP al generar su
JSON Schema.

Query: `table` (`tasks` · `observations` · `worktrees` · `products`).

```json
{
  "total": 1,
  "fields": [
    { "id": "01JB3R9D2K5M7P0S4W6YXVZBNC", "table_name": "observations", "name": "impact", "type": "enum", "required": true, "enum_values": ["low", "medium", "high"], "description": "Cuánto cambia lo que ya estaba decidido", "indexed": true, "position": 1, "created_at": "2026-09-15T10:20:00Z" }
  ]
}
```

Errores: `422` [`invalid_enum`](#invalid_enum) (`table` fuera de las cuatro)

#### `POST /schema/fields`

**N** · Agrega una columna: inserta en `schema_fields`, corre
`ALTER TABLE ADD COLUMN` en el momento, crea el índice si `indexed`, y
regenera el JSON Schema del MCP. `201`.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `table_name` | enum | sí | — |
| `name` | string | sí | — |
| `type` | enum | sí | — |
| `required` | bool | no | `false` |
| `enum_values` | array[string] | sí si `type=enum` | — |
| `description` | string | sí | — |
| `indexed` | bool | no | `false` |

```json
{ "table_name": "observations", "name": "impact", "type": "enum", "enum_values": ["low", "medium", "high"], "required": true, "indexed": true, "description": "Cuánto cambia lo que ya estaba decidido" }
```

```json
{ "id": "01JB3R9D2K5M7P0S4W6YXVZBNC", "table_name": "observations", "name": "impact", "type": "enum", "ddl": "ALTER TABLE observations ADD COLUMN impact TEXT", "index_created": true, "mcp_schema_regenerated": true }
```

Errores: `409` [`field_exists`](#field_exists) ·
`422` [`reserved_name`](#reserved_name) · [`invalid_enum`](#invalid_enum) ·
`400` [`invalid_request`](#invalid_request)

#### `DELETE /schema/fields/{table}/{name}`

**N** · `DROP COLUMN`. No se ejecuta si hay filas con valor no nulo, salvo
`force=true`. `204`.

Query: `force` (default `false`).

Errores: `404` [`not_found`](#not_found) ·
`409` [`field_in_use`](#field_in_use) (el `hint` trae cuántas filas tienen valor)

#### `GET /instructions`

**N** · El bloque que recibe el agente: parte generada desde `schema_fields` y
`gates`, más parte libre. Lo consume `session-start` y
`neugram instructions show`.

Query: `product` (aplica el override), `rendered` (default `true`; `false`
devuelve las partes por separado).

```json
{
  "product": "engram-fork",
  "source": "product_override",
  "generated": "## Tools de flujo\n- task_pin: fija la tarea de la sesión...",
  "free": "## Engram — memoria persistente\n- Toda observación cierra con el pie **Task**...",
  "rendered": "## Tools de flujo\n...\n\n## Engram — memoria persistente\n...",
  "updated_at": "2026-09-18T09:12:44Z"
}
```

`source` es `global` o `product_override`. El override **reemplaza** a la
global; no se concatenan.

En el paso 3 sirve solo la parte generada, sin `schema_fields` (todavía no
existen), y `free` viene vacío; las instrucciones editables entran en el paso 4.

Errores: `404` [`unknown_product`](#unknown_product)

#### `PUT /instructions`

**N** · Guarda la parte libre. Cuerpo de `neugram instructions edit` al cerrar
el `$EDITOR`.

```json
{ "product": "engram-fork", "body_md": "## Engram — memoria persistente\n..." }
```

```json
{ "product": "engram-fork", "source": "product_override", "bytes": 1842, "updated_at": "2026-09-21T16:31:07Z" }
```

`body_md` vacío borra el override de producto, o deja la global vacía con
aviso.

Errores: `404` [`unknown_product`](#unknown_product)

#### `GET /gates`

**N** · Gates con su estado y sus `params`.

```json
{
  "gates": [
    { "name": "require-task-pin-on-mutation", "enabled": true, "params": null, "description": "Bloquea un turno que mutó archivos sin pin de tarea", "updated_at": "2026-09-01T08:00:00Z" },
    { "name": "require-field", "enabled": false, "params": { "field": "impact", "products": ["Portal"] }, "description": "Exige un campo custom en los mem_save del turno", "updated_at": "2026-09-01T08:00:00Z" }
  ]
}
```

Errores: ninguno propio.

#### `PUT /gates/{name}`

**N** · Prende o apaga un gate y fija sus `params`. El set es fijo: un nombre
que no está en él es `404` (ADR 0009).

```json
{ "enabled": true, "params": { "field": "impact", "products": ["Portal"] } }
```

```json
{ "name": "require-field", "enabled": true, "params": { "field": "impact", "products": ["Portal"] }, "updated_at": "2026-09-21T16:35:20Z" }
```

Errores: `404` [`not_found`](#not_found) · `400` [`invalid_request`](#invalid_request)

#### `POST /import/legacy`

**N** · Importa `~/.engram/engram.db` una vez. Idempotente. Corre las cinco
verificaciones de `specs/engram-provenance.md` y aborta si alguna falla.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `db_path` | string | no | `~/.engram/engram.db` |
| `dry_run` | bool | no | `false` |

```json
{
  "dry_run": true,
  "db_path": "/home/rcrespodev/.engram/engram.db",
  "checks": [
    { "name": "schema_columns", "ok": true },
    { "name": "sync_id_unique", "ok": true },
    { "name": "sessions_referenced", "ok": true },
    { "name": "relations_referenced", "ok": true },
    { "name": "scope_and_project", "ok": true }
  ],
  "counts": { "observations": 6805, "sessions": 2313, "prompts": 4650, "relations": 353, "tasks": 0, "worktrees": 0 },
  "inserted": { "observations": 0, "sessions": 0, "prompts": 0, "relations": 0 },
  "skipped_existing": 0,
  "took_ms": 4210
}
```

Con `dry_run: false`, `inserted` trae lo escrito y `skipped_existing` lo que ya
estaba. Una verificación en rojo devuelve `422`
[`import_precheck_failed`](#import_precheck_failed) con el `check` que falló y
cuántas filas lo violan; no es un bug del import, es un dato que la base legacy
tiene y el schema propio no admite.

Errores: `422` [`import_precheck_failed`](#import_precheck_failed) ·
`404` [`not_found`](#not_found) (la base legacy no existe)

#### `GET /doctor`

**N** · Diagnóstico. Cuerpo de `neugram doctor` y de la página `/doctor`.

```json
{
  "status": "ok",
  "db": { "path": "/home/rcrespodev/.neugram/neugram.db", "size_mb": 41.8, "schema_version": 7, "wal": true },
  "fts": { "observations_fts": { "rows": 6923, "source_rows": 6923, "ok": true }, "prompts_fts": { "rows": 4711, "source_rows": 4711, "ok": true } },
  "gates_enabled": ["require-task-pin-on-mutation", "require-save-on-mutation", "require-worktree-registered", "require-progress-report", "block-topic-key-collision"],
  "last_import_at": "2026-09-21T09:40:12Z",
  "orphans": { "observations_without_session": 0, "tasks_with_broken_parent": 0, "worktrees_dead": 3 },
  "warnings": []
}
```

`status` es `ok`, `warn` o `error`; cada entrada de `warnings` trae `code` y
`hint` como cualquier error.

Errores: ninguno propio.

### Hooks

Tres rutas: las que necesitan leer un transcript o devolver markdown para el
agente. Las otras dos entradas de hook no tienen ruta propia —
`user-prompt-submit` usa [`POST /prompts`](#post-prompts) y `session-end` usa
[`POST /sessions/{id}/end`](#post-sessionsidend).

#### `POST /hooks/session-start`

**N** · Arma el bloque markdown que el hook imprime a stdout: upserta la
sesión, junta instrucciones, estado del pin, tareas abiertas, último handoff y
worktrees.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `session_id` | string | sí | — |
| `cwd` | string | sí | — |
| `source` | enum | sí | — |

`source` ∈ `startup` · `resume` · `clear` · `compact` · `fork`. En todos el
`stdout` abre con `CLAUDE_CODE_SESSION_ID=<id>`.

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "cwd": "/home/rcrespodev/rcrespodev/neugram", "source": "startup" }
```

```json
{
  "stdout": "CLAUDE_CODE_SESSION_ID=e8d02241-459e-4faf-9438-165a3062dbb1\n\n## Tools de flujo\n...\n\nPin: TRIPULSE-3652 — Best Skills [in-progress]\n\n## Último handoff\n...",
  "session_created": false,
  "project": "neugram",
  "pin": { "task_key": "TRIPULSE-3652", "status": "in-progress" },
  "open_tasks": 4,
  "handoff_sync_id": "obs-2a7f83c15d0b4e9681c3f70a4b6d29e5",
  "worktrees_alive": 1
}
```

El hook imprime `stdout` tal cual; el resto de las claves son para el log de
`~/.neugram/hooks.log`.

Errores: `400` [`invalid_request`](#invalid_request)

#### `POST /hooks/stop`

**N** · Escanea el transcript desde el último mensaje humano y aplica los
gates habilitados. Un solo bloqueo por turno: el primero que falla.

| Campo | Tipo | Oblig. | Default |
|---|---|---|---|
| `session_id` | string | sí | — |
| `transcript_path` | string | sí | — |
| `stop_hook_active` | bool | no | `false` |

```json
{ "session_id": "e8d02241-459e-4faf-9438-165a3062dbb1", "transcript_path": "/home/rcrespodev/.claude/projects/-home-.../e8d02241.jsonl", "stop_hook_active": false }
```

```json
{
  "decision": "block",
  "reason": "**require-task-pin-on-mutation**\n\nEl turno editó `docs/specs/api.md` y la sesión no tiene pin.\n\nTareas abiertas:\n- TRIPULSE-3652 — Best Skills [in-progress]\n- engram-fork-debat — Fork personal de Engram [in-progress]\n\nDestrabar: `task_pin {\"task_id\": \"<key>\"}` o `task_pin {\"task_id\": \"none\"}`.",
  "gate": "require-task-pin-on-mutation",
  "gates_evaluated": ["require-task-pin-on-mutation"],
  "observed": { "files_mutated": ["docs/specs/api.md"], "saves": 0, "worktrees_created": [], "progress_reports": 0 }
}
```

Sin violaciones, `decision` es `null` y el hook sale con 0 sin salida:

```json
{ "decision": null, "gate": null, "gates_evaluated": ["require-task-pin-on-mutation", "require-save-on-mutation", "require-worktree-registered"], "observed": { "files_mutated": ["docs/specs/api.md"], "saves": 1, "worktrees_created": [], "progress_reports": 0 } }
```

Con `stop_hook_active: true` el hook no llega a llamar: sale con 0 para evitar
el bucle.

Errores: `404` [`unknown_session`](#unknown_session) ·
`400` [`invalid_request`](#invalid_request) (transcript ilegible)

#### `POST /hooks/subagent-stop`

**N** · Igual que `stop` sobre el transcript del subagente, que se lee entero
porque no hay mensajes humanos que marquen el inicio del turno. No aplica
`require-save-on-mutation`; `require-progress-report` solo existe acá.

Mismo payload que [`POST /hooks/stop`](#post-hooksstop), con el
`agent_transcript_path` del `SubagentStop` como `transcript_path` (el
`transcript_path` del evento es el del padre) y un `agent_id` opcional para el
log (`specs/hooks.md#hallazgos-del-spike-de-claude-code`).

En el paso 3 aplica solo `require-task-pin-on-mutation` y
`require-worktree-registered`; `require-progress-report` entra en el paso 4,
con `progress_write`.

```json
{
  "decision": "block",
  "reason": "**require-progress-report**\n\nEl subagente editó `docs/specs/api.md` y no escribió su reporte.\n\nDestrabar: `progress_write {\"delegation_id\": \"api-04\", \"role\": \"impl\", \"status\": \"done\", \"title\": \"…\", \"summary\": [5 a 10], \"next_steps\": [3 a 6]}`.",
  "gate": "require-progress-report",
  "gates_evaluated": ["require-task-pin-on-mutation", "require-worktree-registered", "require-progress-report"],
  "observed": { "files_mutated": ["docs/specs/api.md"], "saves": 0, "worktrees_created": [], "progress_reports": 0 }
}
```

Errores: los de `POST /hooks/stop`.

## Errores

Un solo catálogo para las dos superficies. En HTTP es el cuerpo de toda
respuesta ≥400; en MCP es el contenido de un resultado con `isError: true`.

```json
{
  "error": {
    "code": "no_pin",
    "message": "La sesión no tiene tarea pineada y la llamada no trae task_id.",
    "hint": "task_pin {\"task_id\": \"TRIPULSE-3652\"} o task_pin {\"task_id\": \"none\"}",
    "details": { "open_tasks": ["TRIPULSE-3652 — Best Skills", "engram-fork-debat — Fork personal de Engram"] }
  }
}
```

`code` es estable y es lo único contra lo que se programa. `message` explica
qué pasó; `hint` es la llamada o el comando exacto que destraba, y es
obligatorio en todo error que el agente pueda resolver por su cuenta.
`details` es opcional y su forma depende del `code`.

| `code` | HTTP | Cuándo | Qué trae el `hint` |
|---|---|---|---|
| <a id="no_pin"></a>`no_pin` | 422 | Un método que necesita tarea, sin `task_id` ni pin | `task_pin` con las tareas abiertas en `details` |
| <a id="unknown_task"></a>`unknown_task` | 404 | `key` o id de tarea inexistente. En `task_pin`, además, sin `title` para crearla | `task_pin` con `title`, y las abiertas en `details` |
| <a id="task_exists"></a>`task_exists` | 409 | Alta de una tarea con una `key` que ya existe | `task edit <key>` para cambiar título o producto |
| <a id="invalid_parent"></a>`invalid_parent` | 422 | El padre no existe, es la tarea misma o un descendiente suyo, ya está en el nivel 3, o el subárbol de la tarea a mover desbordaría el tope de 3 niveles | Qué regla se violó, el nivel del padre y los candidatos de nivel 1 o 2 |
| <a id="unknown_product"></a>`unknown_product` | 404 | Nombre de producto inexistente | `neugram product add "<nombre>"` |
| <a id="product_exists"></a>`product_exists` | 409 | Alta de un producto con un nombre que ya existe | `neugram product ls` |
| <a id="unknown_session"></a>`unknown_session` | 404 | `session_id` sin fila en `sessions`. Nadie la crea al vuelo, tampoco el MCP | Que la sesión la crea el hook `session-start` y que el `session_id` es el de su primera línea; si el hook no corrió, recargar la sesión |
| <a id="not_found"></a>`not_found` | 404 | `sync_id`, `judgment_id`, campo o gate inexistente | El listado que muestra los válidos |
| <a id="invalid_request"></a>`invalid_request` | 400 | Payload mal formado, falta un obligatorio, `limit` fuera de rango, `path` relativo, `mem_update` sin ningún campo | El parámetro y qué se esperaba |
| <a id="invalid_type"></a>`invalid_type` | 422 | `type` de observación fuera de la lista fija | Los nueve tipos válidos |
| <a id="invalid_enum"></a>`invalid_enum` | 422 | Valor fuera de `enum_values` o de un enum fijo (`status`, `role`, `relation`, `scope`, `table_name`) | Los valores permitidos |
| <a id="required_field"></a>`required_field` | 422 | Falta un campo custom con `required=1` | El nombre del campo y su `description` de `schema_fields` |
| <a id="invalid_field"></a>`invalid_field` | 422 | `where` o un campo custom nombra una columna que no existe en esa tabla | Las columnas disponibles |
| <a id="invalid_report"></a>`invalid_report` | 422 | `progress_write` con `summary` fuera de 5–10, `next_steps` fuera de 3–6, o `refs.files` sin `path:line` | El campo y su límite |
| <a id="topic_key_collision"></a>`topic_key_collision` | 409 | El gate `block-topic-key-collision` está activo (default `off` en el paso 3, ver `specs/instructions-and-gates.md`) y el `topic_key` ya existe en ese `(project, scope)` con otro `sync_id` | `mem_update` con el `sync_id` existente, que viene en `details` |
| <a id="field_exists"></a>`field_exists` | 409 | `schema add` de un campo ya definido en esa tabla | `neugram schema ls --table <t>` |
| <a id="reserved_name"></a>`reserved_name` | 422 | Nombre de campo custom que colisiona con una columna fija o con `id`, `sync_id`, `created_at`, `updated_at`, `deleted_at` | Los nombres reservados |
| <a id="field_in_use"></a>`field_in_use` | 409 | `schema rm` de una columna con filas de valor no nulo | `--yes --force`, con el conteo de filas en `details` |
| <a id="import_precheck_failed"></a>`import_precheck_failed` | 422 | Una de las cinco verificaciones de procedencia dio rojo | Qué verificación y cuántas filas la violan |
| <a id="internal"></a>`internal` | 500 | Error no previsto; siempre queda en `~/.neugram/hooks.log` | Dónde mirar el log |

Dos errores de la versión con borde HTTP **ya no existen**:
`engram_unavailable` y `engram_rejected`. No hay un segundo servicio que pueda
estar caído ni que pueda rechazar una escritura: el store es código propio en
el mismo proceso (ADR 0011). Un fallo del store es [`internal`](#internal).
