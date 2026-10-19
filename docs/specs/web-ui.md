# Spec — UI web

Servida por `neugram serve` en `http://127.0.0.1:7438/`. Un usuario, sin
login. Server-rendered con templ + htmx, CSS mínimo; referencia de estructura
en `internal/cloud/dashboard` de upstream, sin copiar su estética. Reemplaza lo
único que interesaba de Engram Cloud.

## Páginas

| Ruta | Qué muestra | Acciones |
|---|---|---|
| `/` | Tablero de épicos abiertos agrupados por status, filtro por producto. Por tarjeta: key, título, producto, descendientes (conteo por status, subárbol entero: tareas y sub-tareas), sesiones activas hoy, worktrees vivos, fecha de la última observación | Cambiar status (drag o select) |
| `/tasks/{key}` | Ficha: identidad y tracker con link; breadcrumb de ancestros (`ancestor_keys`, vacío en un épico); árbol de descendientes anidado hasta el nivel 3, con el status de cada uno y link a cada uno; sesiones (con cwd, pin, inicio/fin); worktrees con vivo/muerto y path copiable; timeline de observaciones —de la tarea y de todo su subárbol, cada una etiquetada con su `task_key`— cruzando repos con filtro por tipo y por columna custom; handoffs en orden, el último expandido; reportes de progreso por sesión con rol, status y `delegation_id` | Editar título, producto, campos custom, status, padre (`task mv`, que falla si el subárbol desborda los 3 niveles). Abrir observación |
| `/observations` | Búsqueda FTS con filtros: tarea, producto, repo, tipo, rango de fechas, columnas custom de `observations`. Sin tope de 20 | Abrir, editar `task_id` y custom de una observación (lo que `mem_update` permite) |
| `/observations/{sync_id}` | Content entero renderizado, metadata (tipo, project, custom), vecinas de la misma sesión | Editar `task_id`, `kind`, custom |
| `/sessions/{id}` | Sesión: pin, cwd, prompts (de `user_prompts`), observaciones, handoff si lo hay | Repinear (equivale a `task pin --session`) |
| `/schema` | `schema_fields` por tabla, solo lectura, con el comando CLI para cambiar cada cosa | — |
| `/instructions` | Vista previa del bloque que recibe el agente, global y por producto | — |
| `/doctor` | Lo mismo que `neugram doctor` | — |

## Reglas

- Lo que el ADR 0008 deja solo para CLI (schema, instrucciones, gates) es solo lectura en la UI, con el comando exacto al lado.
- La UI escribe en `neugram.db` como cualquier otro cliente del daemon: editar una observación pasa por el mismo camino HTTP que `mem_update`.
- Sin JavaScript propio más allá de htmx. Sin build step de frontend.
