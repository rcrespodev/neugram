# Features

Estado: `spec` (definida, sin código) · `wip` · `done`. Cada feature apunta a
su spec y al ADR que la justifica. Lo que no está acá está fuera de alcance.

## Núcleo: el eje que Engram no tiene

| # | Feature | Qué entrega | Spec | ADR | Estado |
|---|---|---|---|---|---|
| F1 | Producto → Tarea → Observación | Tablas propias `products` y `tasks`; la tarea absorbe lo que hoy son los registros `identity` y `status` | `specs/data-model.md` | 0002 | wip |
| F2 | Pin de sesión | `sessions.task_id` reemplaza el registro `task/<ID>/session/<uuid>`; tool `task_pin`; pin a `none` explícito | `specs/data-model.md`, `specs/api.md` | 0002, 0008 | wip |
| F3 | Worktrees registrados | Tabla `worktrees` (path, repo, branch, base, purpose, sesión creadora); tool `worktree_register`; liveness la decide el filesystem | `specs/data-model.md` | 0002 | wip |
| F4 | Observaciones con tarea | `task_id` y `kind` son columnas de `observations`, que es tabla propia; el pie `**Task**` se sigue escribiendo en el content porque hace legible la observación fuera de neugram | `specs/data-model.md` | 0002, 0011 | wip |
| F5 | Búsqueda por tarea cruzando repos | `mem_search` y `mem_context` con `task_id` y filtros `where`; FTS5 y columnas en la misma base, un solo query sin topes | `specs/api.md` | 0011 | wip |
| F6 | Handoff tipado | El handoff es una observación con `kind=handoff`, ligada a tarea y sesión; SessionStart entrega el último de la tarea pineada | `specs/hooks.md` | 0002 | wip |
| F18 | Jerarquía de tareas | `tasks.parent_task_id`, tope duro en 3 niveles (épico → tarea → sub-tarea), sin ciclos, con dos triggers no recursivos; `task add --parent`, `task mv --parent` (arrastra el subárbol), `task tree`; la búsqueda sube a los ancestros por default y baja al subárbol con `include_subtasks` | `specs/data-model.md`, `specs/cli.md`, `specs/api.md` | 0012 | spec |

## Extensibilidad

| # | Feature | Qué entrega | Spec | ADR | Estado |
|---|---|---|---|---|---|
| F7 | Columnas custom por CLI | `schema_fields` como fuente única; DDL real (`ALTER TABLE ADD COLUMN`) sobre `tasks`, `observations`, `worktrees` y `products` al arrancar; tipos acotados a SQLite; sin FTS; solo por CLI, nunca por MCP | `specs/data-model.md`, `specs/cli.md` | 0004, 0008 | spec |
| F8 | Instrucciones generadas + libres | El bloque que SessionStart inyecta = parte generada desde `schema_fields` + parte libre editable por CLI, global con override por producto; la sección Engram del CLAUDE.md migra acá | `specs/instructions-and-gates.md` | 0009 | spec |
| F9 | Gates fijos con interruptor | `require-task-pin-on-mutation`, `require-save-on-mutation`, `require-worktree-registered`, `require-field <campo>`, `block-topic-key-collision`, `require-progress-report`; cada uno es código Go más un flag; sin motor de reglas | `specs/instructions-and-gates.md`, `specs/hooks.md` | 0009, 0010 | spec |

## Superficie hacia el agente y el usuario

| # | Feature | Qué entrega | Spec | ADR | Estado |
|---|---|---|---|---|---|
| F10 | MCP espejo extendido | Mismos nombres que Engram (`mem_save`, `mem_search`, …) con `task_id`, campos custom y `where`; más `task_pin`, `task_list`, `task_status`, `worktree_register`, `handoff_save`, `progress_write`, `progress_list`. Único MCP que ve el agente | `specs/api.md` | 0008 | spec |
| F11 | Prompts capturados por hook | `UserPromptSubmit` hace el `POST /prompts`; el agente deja de tener que acordarse | `specs/hooks.md` | 0009 | spec |
| F12 | Hooks como subcomandos | `neugram hook <evento>` para SessionStart, UserPromptSubmit, Stop, SubagentStop, SessionEnd; levantan el daemon si no corre; reemplazan al plugin de hooks de Engram y al Python de engram-tasks | `specs/hooks.md`, `specs/cli.md` | 0006 | spec |
| F13 | CLI de operación | `task`, `product`, `schema`, `instructions`, `gate`, `doctor`, `import-legacy` | `specs/cli.md` | 0006 | wip |
| F14 | UI web local | Tablero de tareas por producto y status, detalle de tarea (sesiones, worktrees vivos/muertos, observaciones, handoffs), búsqueda con filtros por columna; reemplaza lo único que interesaba de Engram Cloud | `specs/web-ui.md` | 0005 | spec |

## Delegación

| # | Feature | Qué entrega | Spec | ADR | Estado |
|---|---|---|---|---|---|
| F17 | Reportes de progreso de subagentes | Tabla `progress_reports` ligada a sesión, tarea y `delegation_id`; tools `progress_write` y `progress_list`; gate `require-progress-report` en `subagent-stop` para subagentes que mutaron. Reemplaza el contrato `progress/<role>_<task-id>.json` del CLAUDE.md | `specs/data-model.md`, `specs/api.md`, `specs/hooks.md` | 0010 | spec |

## Migración

| # | Feature | Qué entrega | Spec | ADR | Estado |
|---|---|---|---|---|---|
| F15 | `import-legacy` | Lee `~/.engram/engram.db` por ATTACH de importación y trae la memoria entera (6.805 observaciones, 2.313 sesiones, 4.650 prompts, 353 relaciones) más el flujo del project `tasks`; idempotente y con `--dry-run` | `specs/import-legacy.md` | 0007, 0011 | done |
| F16 | Corte duro | Un solo cambio en `~/.claude/settings.json`: sale el MCP de Engram, entra `neugram mcp`; salen los tres hooks legacy, entran `neugram hook <evento>`; se apaga el daemon en `:7437`. `~/.engram/engram.db` queda como archivo histórico | `specs/import-legacy.md` | 0007, 0011 | spec |

## Fuera de alcance (decidido)

- Copiar código de Engram, aunque sea el núcleo de memoria. El store se escribe a mano contra `specs/data-model.md`; Engram se lee como referencia y no se usa como cantera (ADR 0011). Si durante la implementación se copia un fragmento, vuelve la atribución MIT para ese archivo (`specs/engram-provenance.md`).
- Seguir a upstream. Sin código compartido no hay nada que mergear: un arreglo de upstream se lee y se reimplementa (ADR 0011).
- Reimplementar lo que Engram tiene y neugram no quiere: cloud, sync, TUI Bubbletea, export a Obsidian, instalador de plugin y su MCP (ADR 0005, 0008, `specs/engram-provenance.md`).
- Convivencia de dos bases, dos daemons o dos puertos (ADR 0011).
- Sync propio, multiusuario, Engram Cloud. Un usuario, datos locales (ADR 0005).
- Motor de reglas declarativo para gates (ADR 0009).
- Cambiar schema o instrucciones desde el MCP: el agente no edita sus propias reglas (ADR 0008).
- Atributos JSON o EAV para columnas custom (ADR 0004).
- Convivencia de dos MCP durante la migración (ADR 0007).
- Indexar columnas custom en FTS (ADR 0004).
