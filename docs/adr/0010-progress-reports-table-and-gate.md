# ADR 0010 — Reportes de progreso de subagentes: tabla propia, tool y gate en SubagentStop

Estado: aceptado · Origen: obs 6721 (project `neugram`)

## Contexto

La sección Delegation del CLAUDE.md pide que cada subagente escriba
`progress/<role>_<task-id>.json` en el cwd o en la raíz del worktree, con
escritura atómica y claves fijas (`feature`, `status`, `references`, `summary`,
`next_steps`), y que devuelva al padre solo la ruta, el status y la próxima
acción. Es prosa: ningún hook verifica que el archivo exista, que sea JSON ni
que tenga las claves. El único lector es `/handoff`, que hace
`ls <wt>/progress/`; el reporte muere con el worktree. Además `<task-id>` es un
id por delegación que asigna el padre, y en neugram "tarea" es el ticket.

## Decisión

- El reporte es una fila de una tabla propia, `progress_reports`, ligada a sesión y tarea. No es archivo ni observación de Engram: es estado de trabajo que un hook consulta, no memoria.
- El `<task-id>` del contrato viejo pasa a llamarse `delegation_id`. La clave `feature` (`<id> — <name>`) desaparece: la tarea sale del pin y el nombre pasa a `title`.
- El subagente lo escribe con la tool `progress_write`; el padre y `/handoff` lo leen con `progress_list`. neugram valida forma y límites; la escritura atómica deja de ser responsabilidad del agente.
- Gate nuevo `require-progress-report`, evaluado solo en `subagent-stop`: bloquea al subagente que mutó archivos y no llamó a `progress_write`. Un subagente que no mutó no está obligado.
- La política de delegación (cuándo delegar, tope de subagentes, modelos) sigue en el CLAUDE.md. Solo migra el contrato del artefacto.

## Consecuencias

- `stop_hook_active` corta el segundo bloqueo, así que el gate da exactamente un reintento, que es lo que la regla escrita pedía.
- Un subagente que puede mutar necesita `progress_write` entre sus tools; si su definición la excluye, el gate lo bloquea sin salida. El `reason` lo dice.
- Los agentes de solo lectura siguen devolviendo inline; pueden usar `progress_write` si la tienen, pero nadie los obliga.
- Los `progress/*.json` existentes no se importan: son efímeros y su contenido útil ya pasó a los handoffs.
- La ficha de tarea de la UI y `task_show` muestran los reportes; sobreviven al worktree.
- Depende de que el input de `SubagentStop` permita leer el transcript del subagente y no el del padre (deuda en `roadmap.md`).
