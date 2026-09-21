# Spec — UI web

Servida por `neugram serve` en `http://127.0.0.1:7438/`. Un usuario, sin
login. Server-rendered con templ + htmx, CSS mínimo; referencia de estructura
en `internal/cloud/dashboard` de upstream, sin copiar su estética. Reemplaza lo
único que interesaba de Engram Cloud.

## Páginas

| Ruta | Qué muestra | Acciones |
|---|---|---|
| `/` | Tablero de tareas abiertas agrupadas por status, filtro por producto. Por tarjeta: key, título, producto, sesiones activas hoy, worktrees vivos, fecha de la última observación | Cambiar status (drag o select) |
| `/tasks/{key}` | Ficha: identidad y tracker con link; sesiones (con cwd, pin, inicio/fin); worktrees con vivo/muerto y path copiable; timeline de observaciones cruzando repos con filtro por tipo y por columna custom; handoffs en orden, el último expandido; reportes de progreso por sesión con rol, status y `delegation_id` | Editar título, producto, campos custom, status. Abrir observación |
| `/observations` | Búsqueda FTS con filtros: tarea, producto, repo, tipo, rango de fechas, columnas custom de `observation_ext`. Sin tope de 20 | Abrir, editar `task_id` y custom de una observación (lo que `mem_update` permite) |
| `/observations/{sync_id}` | Content entero renderizado, metadata de Engram y de neugram, vecinas de la misma sesión | Editar `task_id`, `kind`, custom |
| `/sessions/{id}` | Sesión: pin, cwd, prompts (de `engram.user_prompts`), observaciones, handoff si lo hay | Repinear (equivale a `task pin --session`) |
| `/schema` | `schema_fields` por tabla, solo lectura, con el comando CLI para cambiar cada cosa | — |
| `/instructions` | Vista previa del bloque que recibe el agente, global y por producto | — |
| `/doctor` | Lo mismo que `neugram doctor` | — |

## Reglas

- Lo que el ADR 0008 deja solo para CLI (schema, instrucciones, gates) es solo lectura en la UI, con el comando exacto al lado.
- Ninguna acción de la UI escribe en `engram.db`: editar una observación pasa por el mismo camino HTTP que `mem_update`.
- Sin JavaScript propio más allá de htmx. Sin build step de frontend.
