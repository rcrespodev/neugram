# ADR 0002 — Eje Producto → Tarea → Observación con tablas propias

Estado: aceptado · Origen: obs 6562, 6563, 6564

## Contexto

La búsqueda más usada es "todo lo de una tarea cruzando repos", que
`mem_context` por cwd no responde. En el project `tasks` había 258 registros de
control sobre 28 tareas (97 pins, 66 worktrees, 44 handoffs, 24 identities,
20 status), todos codificados en `topic_key` y parseados por Python.

## Decisión

Tablas de primera clase: `products`, `tasks`, `worktrees`, `sessions`.
Relaciones: producto 1:N tarea; tarea 1:N observación (`task_id` nullable, que
equivale al `ninguna` actual); tarea 1:N sesión vía `sessions.task_id`, que es
el pin. `identity` y `status` desaparecen como registros: son la fila de
`tasks`. Solo el handoff sigue siendo observación, marcado `kind=handoff`.

Regla: si un hook o CLI consulta el dato, es columna; si es prosa que solo un
agente lee, es observación.

## Consecuencias

- El pin, el status y los worktrees dejan de depender de que el agente escriba un `topic_key` con la forma correcta.
- Repo y sesión pasan a ser metadata de procedencia, no jerarquía.
- Se descartó "todo configurable" (motor de entidades) y "solo campos sin entidades".
