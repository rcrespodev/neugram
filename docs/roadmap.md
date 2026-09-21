# Roadmap

Pasos en orden. Cada uno termina con una verificación concreta contra datos
reales y sin tocar el flujo actual hasta el paso 4. Estado en `features.md`.

| Paso | Entrega | Verifica | Features |
|---|---|---|---|
| 0 | `go mod init`, layout `cmd/neugram`, `internal/{store,engram,server,mcp,hooks,cli}`, `neugram version`, `neugram doctor` con `GET /health` y `PRAGMA table_info` del ATTACH | `neugram doctor` reporta las columnas asumidas en `specs/engram-boundary.md`; confirma nombre de la tabla FTS y el parámetro de `GET /project/current` | — |
| 1 | `serve` con ATTACH read-only y `mem_search` que filtra por `task_id` derivado en caliente de los pies `**Task**` (sin tablas propias todavía) | Buscar `engram-fork-debat` devuelve las 10 observaciones de diseño cruzando repos, con content entero | F5 |
| 2 | Tablas `products`, `tasks`, `worktrees`, `sessions`, `observation_ext`; `import-legacy --dry-run` y real; CLI `task ls/show/wt`, `product ls` | El resumen del import coincide con los conteos de `specs/import-legacy.md`; `task ls` reproduce la lista de tareas abiertas que hoy imprime `engram-tasks-session` | F1 F2 F3 F4 F6 F13 F15 |
| 3 | `mem_save` proxeado con `sync_id` resuelto y `observation_ext`; `task_pin`, `task_status`, `worktree_register`, `handoff_save`; hooks `session-start`, `user-prompt-submit`, `stop`, `subagent-stop`, `session-end` con los gates default; **corte** de MCP y hooks | Una sesión completa en un repo real: arranca, pinea, edita, guarda, cierra con handoff, y la siguiente sesión recibe ese handoff. El gate bloquea una mutación sin pin. Confirmar payload de `POST /conflicts/scan` | F9 F10 F11 F12 F16 |
| 4 | `schema_fields` con DDL, JSON Schema y validación; instrucciones generadas + libres con override; `gate ls/enable/disable`; migración de la sección Engram del CLAUDE.md; `progress_reports`, `progress_write`, `progress_list` y el gate `require-progress-report` | `schema add observation_ext impact enum --enum low,medium,high --required` hace que `mem_save` sin `impact` falle con `required_field` y que `instructions show` lo describa. Un subagente que edita un archivo sin `progress_write` es bloqueado una vez por `subagent-stop`; uno que solo lee termina sin bloqueo | F7 F8 F9 F17 |
| 5 | UI web | Tablero, ficha de tarea y búsqueda funcionan sobre los datos importados | F14 |
| 6 | Apagar Engram Cloud local; retirar el worktree `engram-tasks` y sus 1.521 líneas de Python/bash | `docker compose down`; `neugram doctor` limpio; los skills `/handoff`, `/daily`, `review-wt` funcionan con `task_show`, `handoff_save` y `progress_list` (ya no `ls progress/`) | — |

## Deuda conocida al arrancar

- El envelope `judgment_required/candidates` depende de que `POST /conflicts/scan` acote a una observación. Si no, la primera versión de `mem_save` no lo devuelve.
- La detección de `project` en `POST /sessions` debe coincidir con la de Engram; si `GET /project/current` no recibe un cwd, se reimplementa la misma regla (git remote → basename) y se marca como contrato.
- El checkout de referencia de upstream está en `36c0819` (2026-06-14). Antes del paso 3, actualizarlo y repetir `doctor` contra la versión que realmente corre.
- El gate `require-progress-report` necesita que el input de `SubagentStop` identifique el transcript del subagente (no el del padre). Verificarlo contra la versión de Claude Code que corre antes del paso 4; si no, el gate no se puede evaluar y F17 queda sin enforcement.
