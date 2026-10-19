# Spec — `import-legacy` y corte

Trae `~/.engram/engram.db` adentro de `~/.neugram/neugram.db`: primero la
memoria entera, después el flujo que hoy vive codificado en `topic_key` del
project `tasks`. Una sola corrida. La base legacy no se toca nunca.

## Fuente

```
ATTACH DATABASE 'file:~/.engram/engram.db?mode=ro' AS legacy;
```

Es un ATTACH de importación, no de operación: vive lo que dura el comando y no
vuelve a abrirse (ADR 0011). No hay HTTP de por medio, así que no hay tope de
20 resultados ni filtros que el endpoint ignora en silencio. Engram usa WAL: el
lector necesita `engram.db-wal` y `engram.db-shm`, que en la misma máquina y
usuario tiene. Con el daemon de Engram apagado —que es el orden recomendado— no
hay escritor concurrente.

Antes de escribir nada corren las seis verificaciones de
`specs/engram-provenance.md`. Si una falla, el comando aborta entero.

La sexta es propia de este import y es la más importante: recomputar
`normalized_hash` sobre una muestra de las observaciones legacy y abortar si
alguna no coincide con el valor almacenado. El store es código propio y ya no
hereda la función que produjo esos hashes (ADR 0011), así que es el único
invariante donde el dato legacy depende de una semántica del código viejo. La
reconstrucción es minúsculas, colapsar espacios, sha256; hoy da 3.000/3.000 con
0 discrepancias. Una sola discrepancia significa que el dedupe de todo lo
importado queda roto en silencio —dos observaciones idénticas con hashes
distintos no se reconocen nunca—, y por eso aborta en vez de avisar.

Medido el 2026-09-21 sobre 64 MB: 6.805 observaciones, 2.313 sesiones, 4.650
prompts, 353 relaciones, 331 registros en el project `tasks`.

## Fase 1 — memoria

Copia fila a fila, preservando `id`, `sync_id` y timestamps. El orden lo impone
la FK: `sessions` → `observations` y `user_prompts` → `memory_relations`.

| Origen | Destino | Nota |
|---|---|---|
| `legacy.sessions` | `sessions` | `created_at = started_at`; `task_id`/`pinned_at` los llena la fase 2 |
| `legacy.observations` | `observations` | Todas salvo las 269 de control (abajo). Los soft-deletes se copian con su `deleted_at` |
| `legacy.user_prompts` | `user_prompts` | |
| `legacy.memory_relations` | `memory_relations` | |

Las tablas de sync y cloud no se copian: `sync_mutations` (27.755 filas, 21,7 MB
de los 64), `sync_state`, `sync_chunks`, `sync_enrolled_projects`,
`sync_apply_deferred`, `cloud_upgrade_state`, `prompt_tombstones`. Las columnas
`embedding*` y `expires_at` tampoco, porque no tienen una sola fila con valor.

El FTS no se copia: se reconstruye con `INSERT INTO observations_fts(observations_fts)
VALUES('rebuild')` al final, y lo mismo `prompts_fts`. Es la única forma de que
el índice quede consistente con los `rowid` copiados.

## Fase 2 — flujo

Los 331 registros del project `tasks` no entran como observaciones: su
contenido es el estado que las columnas van a guardar. La excepción son los
handoffs, que son memoria de verdad.

Regex sobre `topic_key`, contra la forma que escribieron los hooks legacy:
`identity` y `status` sin sufijo; `session`, `wt` y `handoff` con sufijo
obligatorio. Cuando hay varios registros para la misma key gana el más nuevo
por `id`.

| `topic_key` | Medido | Destino | Mapeo |
|---|---|---|---|
| `task/<id>/identity` | 28 | `tasks` | `**Task**: <KEY> — <título>` → `key`, `title`; `**Tracker**: clickup · id <x> · <url>` → `tracker_kind/id/url`; `**Product**` → `products` (ver abajo). Lo demás del content → `notes`. La `key` sale del pie, con mayúsculas, no del `topic_key` |
| `task/<id>/status` | 23 | `tasks.status` | Primera palabra del content si está en el enum; si no, `todo` y aviso. Valores presentes: 13 `review`, 6 `in-progress`, 3 `done`, 1 `todo` |
| `task/<id>/session/<uuid>` | 126 | `sessions` | `UPDATE sessions SET task_id, pinned_at=created_at, pinned_by='import' WHERE id=<uuid>`. 25 pinean a `none`: `task_id NULL`, `pinned_at` igual. Todos los uuid existen en `legacy.sessions` |
| `task/<id>/wt/<basename>` | 83 | `worktrees` | Fence ```json con `path, repo, branch, base, created_by_session, purpose`. Los viejos con `**Path**: …` / `Path:` / `- Path:` se parsean línea a línea. Upsert por `path`. 82 tienen path parseable: 62 vivos en disco, 20 muertos |
| `task/<id>/handoff/<uuid>` | 62 | `observations` | Se importa como observación con `kind='handoff'`, `task_id`, `session_id=<uuid>` y `project` reescrito al de esa sesión, no `tasks`. Los 62 uuid existen |
| otros | 9 | — | Se cuentan y se listan por `topic_key`; no se importan |

Una tarea referida por un pin, un wt o un handoff sin registro `identity` se
crea con `title = key` y `status = todo`, y se lista al final como "sin
identity". Las keys de `topic_key` son 31; 28 tienen identity.

### Jerarquía

Todas las tareas importadas entran como épicos: `parent_task_id NULL`, nivel 1.
El legacy no tiene dónde declarar un padre —`topic_key` es plano y el pie
`**Task**` nombra una sola key—, así que no hay árbol que reconstruir y los
triggers de profundidad de `specs/data-model.md` no se disparan ni una vez.

Hay un indicio y se deja pasar a propósito: cuatro identities meten el padre
adentro de la prosa de `**Product**` (`Microservices (react) · parent
TRIPULSE-3652`, `Portal / Administración Clientes (padre TRIPULSE-3242)`).
Es el mismo caso que el producto —prosa libre, no contrato— y la decisión es la
misma: el import no adivina. La cadena original entera queda en `tasks.notes`,
así que el indicio sobrevive al import y el resumen lo lista. Armar el árbol es
trabajo manual del usuario con `task mv <key> --parent <key>` después del paso
1b, que es exactamente para lo que ese comando existe.

### Productos

`**Product**` es prosa libre: 28 identities traen 23 valores distintos para lo
que son un puñado de productos (`Microservices (react) · parent TRIPULSE-3652`,
`Portal / Administración Clientes (padre TRIPULSE-3242)`). El import normaliza
tomando el segmento anterior al primer ` · `, ` — `, ` / ` o ` (`, con lo que
quedan 13, y guarda la cadena original completa en `tasks.notes`. El resumen
imprime el mapeo entero para que el usuario consolide después con el CLI.
Ninguna heurística acierta acá: la decisión es que el import no pierda el dato
original y no pretenda adivinar.

## Fase 3 — pies `**Task**`

Para cada observación importada con `project != 'tasks'` cuya última línea no
vacía matchea `**Task**:`, se llena `observations.task_id`.

El pie tiene dos formas en los datos: `**Task**: <KEY> — <título>` y
`**Task**: <clickup-id> · <KEY> — <título>`. El parser corta por ` — `, y si lo
que queda tiene un ` · `, se queda con el segmento de la derecha. Sin esa
segunda forma quedan 10 keys sin resolver y 279 observaciones sin tarea;
con ella, 25 keys y una sola observación irresoluble (`pendiente de decidir`).

Medido: 1.195 observaciones con pie en la última línea. 1.059 con key, 136 con
`ninguna` — que insertan `task_id NULL`, indistinguible de una observación sin
pie en la columna, y es correcto: las dos significan "sin tarea". Una key que
no existe en `tasks` se crea como arriba.

El pie se sigue escribiendo en el content de las observaciones nuevas
(`specs/api.md`), redundante con la columna, porque es lo que hace legible
una observación fuera de neugram.

`worktrees.created_by_session` no lleva FK: de los 82 registros con path, uno
apunta a una sesión que no está en `legacy.sessions`. Es el único lugar donde
los datos legacy no aguantan la integridad referencial, y se resuelve
guardando el uuid sin exigirlo.

## Idempotencia

Cada destino se upsertea por su clave natural: `sessions.id`,
`observations.sync_id`, `user_prompts.sync_id`, `memory_relations.sync_id`,
`tasks.key`, `worktrees.path`, `products.name`. Correr dos veces da el mismo
resultado. `meta.last_import_at` y `meta.legacy_db_path` guardan la corrida.

## Salida

`--dry-run` imprime el mismo resumen sin escribir:

```
sesiones      2.313
observaciones 6.536   (269 registros de control no importados como observación)
prompts       4.650
relaciones      353   (334 juzgadas, 19 pendientes)
tareas           31   28 con identity · 3 creadas desde pin/wt/handoff · 31 como épico
                      4 con indicio de padre en **Product**: <lista de keys>
productos        13   (desde 23 cadenas; mapeo abajo)
pins            126   (101 con tarea, 25 none)
worktrees        82   (62 vivos, 20 muertos) · 1 registro sin path parseable
handoffs         62   vinculados a tarea y sesión
pies          1.195   1.059 con tarea · 136 ninguna · 1 sin key resoluble
otros             9   registros ignorados: <lista de topic_keys>
hash          3.000   normalized_hash recomputado · 0 discrepancias
```

## Corte

Después de un `import-legacy` real y un `neugram doctor` limpio, un solo edit a
`~/.claude/settings.json` y `~/.claude.json`:

1. Quitar el MCP `plugin_engram_engram` (o deshabilitar el plugin) y agregar `neugram` con `command: neugram, args: [mcp]`.
2. Quitar los hooks `~/.claude/hooks/engram-tasks-session`, `~/.claude/hooks/engram-save-gate`, `~/.claude/hooks/engram-record-gate` y los del plugin de Engram; agregar los cinco de `specs/hooks.md`.
3. Reemplazar la sección `## Engram — persistent memory` del CLAUDE.md global por dos líneas que apunten a `neugram instructions show`. Es el mismo trabajo que F8 (migrar instrucciones a la tabla `instructions`), no un paso aparte: toda la sección queda vencida por la absorción — el `id` de observación/prompt ya no es clave pública (`sync_id` lo es), los registros de control en el project `tasks` los reemplazan `tasks`/`worktrees`/`progress_reports`, y la lectura HTTP ya no es `127.0.0.1:7437`.
4. Apagar el daemon de Engram en `:7437` y el stack Engram Cloud (`docker compose -f ~/.claude/plugins/marketplaces/engram/docker-compose.cloud.yml down`). Desde acá `~/.engram/` es un archivo histórico: nada lo abre salvo un import repetido.

Los skills `/handoff`, `/daily` y `review-wt` se adaptan en el mismo commit:
`/handoff` llama `handoff_save`; `/daily` y `review-wt` leen `task_show` en vez
de parsear `identity`.

Vuelta atrás: revertir el commit de config y volver a levantar el daemon de
Engram. `~/.engram/engram.db` quedó intacto, así que lo anterior al corte sigue
entero; lo guardado con neugram entre el corte y la vuelta queda en
`neugram.db` y no viaja de vuelta — ese es el precio del corte duro (ADR 0007).
