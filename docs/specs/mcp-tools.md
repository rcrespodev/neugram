# Spec — Tools MCP

Servidor: `neugram mcp` (stdio). Cliente fino del HTTP propio en `:7438`; toda
la lógica está en el daemon. Es el único MCP de memoria que ve el agente tras
el corte.

Convenciones:

- `task_id` en cualquier tool acepta la `key` humana (`TRIPULSE-3652`, sin distinguir mayúsculas) o `none`. Si se omite, se usa el pin de la sesión. Sin pin y sin `task_id` la tool falla con un mensaje que nombra `task_pin`.
- Los campos custom de `schema_fields` aparecen en el JSON Schema de la tool que corresponde (`mem_save`/`mem_update` para `observation_ext`, `task_pin`/`task_list` para `tasks`, `worktree_register` para `worktrees`), con `description` y `enum` tomados de la fila. `required=1` los hace obligatorios en `mem_save`.
- `where` es un objeto `{columna: valor}` sobre columnas fijas o custom de la tabla consultada. Igualdad exacta; `null` filtra `IS NULL`. Sin operadores en la primera versión.
- `session_id` se toma de `CLAUDE_CODE_SESSION_ID` en el entorno del proceso MCP. Ninguna tool lo pide.
- Los envelopes conservan las claves que Engram usa (`result`, `project`, `id`, `sync_id`) y agregan `task_id`, `task_key`.

## Tools espejo (nombres de Engram)

| Tool | Parámetros | Comportamiento |
|---|---|---|
| `mem_save` | `title`*, `content`*, `type`*, `task_id`, `topic_key`, `scope`, custom de `observation_ext` | Valida `type` contra la lista fija, required de schema; agrega el pie `**Task**` al content si falta o lo corrige si no coincide; `POST /observations`; resuelve `sync_id`; inserta `observation_ext`. Envelope: `{id, sync_id, task_id, task_key, judgment_required, candidates}`; los dos últimos solo si `/conflicts/scan` los da |
| `mem_update` | `id`* o `sync_id`*, `title`, `content`, `type`, `topic_key`, `task_id`, custom | `PATCH /observations/{id}` para los campos de Engram; `UPDATE observation_ext` para los propios. Si cambia `task_id`, reescribe el pie |
| `mem_search` | `query`*, `task_id`, `product`, `project`, `type`, `where`, `limit` (máx 50), `full` | FTS attacheada + JOIN con `v_observations`. `full=true` devuelve el content entero; default preview 300 chars. `task_id` cruza repos |
| `mem_context` | `task_id`, `project`, `limit` | Sin `task_id`: como Engram, últimas observaciones del repo del cwd. Con `task_id`: últimas de la tarea en todos los repos, más su último handoff y sus worktrees vivos |
| `mem_get_observation` | `id`* o `sync_id`* | Fila completa de `v_observations` con custom |
| `mem_save_prompt` | `content`* | Existe por compatibilidad; el hook ya lo hizo. Devuelve el id del prompt más reciente de la sesión si el content coincide, o guarda si no |
| `mem_judge` | `judgment_id`*, `relation`*, `reason`, `evidence`, `confidence` | Proxy a `POST /conflicts/judge` con `session_id` |
| `mem_session_summary` | `content`* | Guarda como observación `type=session_summary` con el pin. El cierre normal es `handoff_save` |
| `mem_timeline` | `id`* o `sync_id`*, `before`, `after` | Observaciones vecinas de la misma sesión, por SQL |
| `mem_review` | `action` (`list`/`mark_reviewed`), `project`, `id` | `list`: por SQL sobre `review_after` si la columna existe en el ATTACH; `mark_reviewed`: proxy HTTP `POST /review/mark_reviewed` |
| `mem_current_project` | — | Repo del cwd según Engram, más producto y tarea pineada |

No se espejan en la primera versión: `mem_delete`, `mem_pin`/`mem_unpin`,
`mem_merge_projects`, `mem_stats`, `mem_doctor`, `mem_compare`,
`mem_capture_passive`, `mem_session_start`/`mem_session_end`,
`mem_suggest_topic_key`. Las de sesión las cubren los hooks; el resto es
operación y va por CLI o UI.

## Tools de flujo (propias)

| Tool | Parámetros | Comportamiento |
|---|---|---|
| `task_pin` | `task_id`* (`key` o `none`), `title`, `product`, custom de `tasks` | Fija `sessions.task_id` y `pinned_at` para la sesión. Si la `key` no existe y viene `title`, crea la tarea con `status=todo`; sin `title`, falla listando las abiertas. Repetir con otra tarea repinea y lo deja en el log. `pinned_by=agent` |
| `task_list` | `status`, `product`, `where`, `include_done` | Tareas con producto, status, cantidad de sesiones y worktrees vivos, fecha de la última observación |
| `task_status` | `task_id`, `status`* | Cambia `tasks.status`. Lo declara el usuario a través del agente; el hook nunca lo llama |
| `task_show` | `task_id` | Fila de la tarea, sesiones, worktrees con liveness, últimos handoffs, reportes de progreso de la última sesión |
| `worktree_register` | `path`*, `repo`*, `branch`*, `base`, `purpose`, `task_id` | Upsert por `path`. `created_by_session` = sesión actual. Es lo que el gate `require-worktree-registered` exige |
| `handoff_save` | `content`*, `task_id` | `mem_save` con `type=handoff`, `kind=handoff` en `observation_ext`, `title` = `Handoff <key>: <primera línea del State>`. Uno por sesión: repetir lo actualiza |
| `progress_write` | `delegation_id`*, `role`* (`explore`/`impl`/`review`), `status`* (`done`/`blocked`), `title`*, `summary`* (5–10 strings), `next_steps`* (3–6 strings), `refs` (`engram_topics`, `files` como `path:line`, `links`) | Upsert en `progress_reports` por `(session_id, delegation_id)`. `task_id` = pin de la sesión; `worktree_id` = worktree registrado que contiene el cwd. Devuelve `{id, delegation_id, status}`, que es todo lo que el subagente le pasa al padre. Es lo que el gate `require-progress-report` exige |
| `progress_list` | `delegation_id`, `task_id`, `session_id`, `status`, `limit` | Reportes completos, más nuevos primero. Sin filtros: los de la sesión actual. Con `task_id`: todas las sesiones de la tarea |

## Lo que el MCP no expone

`schema add/rm`, `instructions edit`, `gate enable/disable`, `import-legacy`,
`product add`. Son CLI (ADR 0008). Si el agente los pide, la respuesta es el
comando que el usuario tiene que correr.

## Errores

Todos con `code` estable y `hint` con la llamada que destraba:

| `code` | Cuándo |
|---|---|
| `no_pin` | Tool que necesita tarea, sin `task_id` ni pin |
| `unknown_task` | `key` inexistente y sin `title` para crearla |
| `required_field` | Falta un custom `required=1`; `hint` nombra el campo y su descripción |
| `invalid_enum` | Valor fuera de `enum_values` |
| `invalid_report` | `progress_write` con `summary` o `next_steps` fuera de rango, o `role`/`status` inválido; `hint` nombra el campo y el límite |
| `topic_key_collision` | Gate activo y el `topic_key` ya existe en el project |
| `engram_unavailable` | `:7437` no responde; la lectura sigue disponible |
| `engram_rejected` | Engram devolvió 4xx; se reenvía su `error` y `code` |
