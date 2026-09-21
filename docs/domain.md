# Dominio

Vocabulario fijo. Si un término de acá se usa con otro sentido en código o docs,
el error está en el código o en la doc.

| Término | Definición | Dónde vive |
|---|---|---|
| **Producto** | Lo que el usuario llama "proyecto": Microservices, Portal, engram-fork. Agrupa tareas. Nunca es un repo | `products` |
| **Repo** | Lo que Engram llama `project`: derivado del git remote o del directorio. Metadata de procedencia de una observación, no jerarquía | `engram.observations.project` |
| **Tarea** | Unidad de trabajo con id humano (`TRIPULSE-3652`, `CLIENTE-1542`, `engram-fork-debat`), título, producto opcional, status y tracker opcional | `tasks` |
| **Status** | Una palabra declarada por el usuario: `todo` · `in-progress` · `review` · `blocked` · `done`. Ningún hook lo cambia | `tasks.status` |
| **Sesión** | Una sesión de Claude Code, identificada por `CLAUDE_CODE_SESSION_ID`. Los subagentes heredan el id | `sessions` (propia) y `engram.sessions` |
| **Pin** | La relación sesión → tarea, fijada antes de la primera mutación. `none` es un pin válido y explícito, distinto de "sin pin" | `sessions.task_id` + `sessions.pinned_at` |
| **Mutación** | Un `Edit`, `Write` o `Bash` que cambia archivos dentro de un turno. Lo detecta el hook `Stop` leyendo el transcript | — |
| **Observación** | Lo que Engram guarda: decisión, bugfix, descubrimiento, etc. Sigue en Engram | `engram.observations` |
| **Extensión de observación** | La fila 1:1 que neugram mantiene por `sync_id`: `task_id`, `kind` y columnas custom | `observation_ext` |
| **Pie `**Task**`** | La última línea del content de una observación: `**Task**: <ID> — <título>` o `**Task**: ninguna — <por qué>`. Redundante con `observation_ext.task_id`; se conserva para que Engram lo muestre | content |
| **Handoff** | Observación de cierre de sesión que la sesión siguiente lee al arrancar. `kind=handoff`, con tarea y sesión | `engram.observations` + `observation_ext` |
| **Worktree** | Un `git worktree` creado para una tarea. Registrado con path, repo, branch, base, propósito y sesión creadora. Vivo si el path existe | `worktrees` |
| **Campo custom** | Columna que el usuario agregó a `tasks`, `observation_ext`, `worktrees` o `products` por CLI. Fuente única: `schema_fields` | `schema_fields` + DDL |
| **Instrucciones** | El bloque markdown que SessionStart inyecta al agente: parte generada desde `schema_fields` + parte libre. Global con override por producto | `instructions` |
| **Delegación** | Un encargo que el agente padre le da a un subagente dentro de la misma sesión. No es una tarea: una tarea tiene muchas delegaciones | — |
| **`delegation_id`** | Id corto que el padre asigna a cada delegación y le pasa al subagente (`explore-auth`, `impl-2`). Único dentro de la sesión. En el CLAUDE.md legacy se llamaba `task-id`; ese nombre no se usa en neugram | `progress_reports.delegation_id` |
| **Reporte de progreso** | Lo que un subagente deja al terminar una delegación: rol, status, resumen, próximos pasos y referencias. El padre lo lee en vez de recibir el volcado inline | `progress_reports` |
| **Gate** | Una comprobación fija que el hook `Stop` o `SubagentStop` aplica y que devuelve un decision block si falla. Se prende o apaga; no se programa | `gates` |
| **Corte** | El momento en que el MCP y los hooks de Engram salen de `settings.json` y entran los de neugram. Es un solo commit de config | — |

## Reglas derivadas

- Si un hook o el CLI consulta el dato, es columna. Si es prosa que solo un agente lee, es observación.
- Una observación pertenece a cero o una tarea, nunca a varias (medido: 1.196 observaciones de `react`, ninguna con dos ids).
- El control de flujo (pin, worktree, status) nunca viaja como `mem_save` con `topic_key` mágico. `topic_key` queda para observaciones que evolucionan.
- El agente no puede cambiar schema, instrucciones ni gates. Eso es CLI.
- "Tarea" y `task_id` nombran siempre el ticket. Lo que el padre asigna a un subagente es una delegación y su id es `delegation_id`.
