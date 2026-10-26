# Spec — `import-legacy` y corte

Trae `~/.engram/engram.db` adentro de `~/.neugram/neugram.db`: primero la
memoria entera, después el flujo que hoy vive codificado en `topic_key` del
project `tasks`. Una sola corrida. La base legacy no se toca nunca.

## Fuente

```
ATTACH DATABASE 'file:~/.engram/engram.db?mode=ro' AS legacy;
```

`--engram-db <path>` cambia el archivo legacy (default `~/.engram/engram.db`) y
`--db <path>` la base destino (default `~/.neugram/neugram.db`); con los dos se
verifica contra una copia sin tocar ninguna de las dos bases reales.

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

La muestra es la tabla entera: recomputar 8.571 hashes tarda menos que elegir
cuáles, así que la verificación 6 cubre todas las filas con `normalized_hash`.

Junto a las seis corre un gate de timestamps: todo valor de tiempo legacy tiene
que parsear como `YYYY-MM-DD HH:MM:SS` o `YYYY-MM-DDTHH:MM:SSZ`; uno que no
parsea aborta igual que una verificación, con el conteo por `tabla.columna`.

Medido el 2026-09-21 sobre 64 MB: 6.805 observaciones, 2.313 sesiones, 4.650
prompts, 353 relaciones, 331 registros en el project `tasks`.

## Fase 1 — memoria

Copia fila a fila, preservando `id`, `sync_id` y timestamps. El orden lo impone
la FK: `sessions` → `observations` y `user_prompts` → `memory_relations`.
Preservar un timestamp es preservar el instante, no el texto: cada columna de
tiempo legacy (`YYYY-MM-DD HH:MM:SS`, UTC) se normaliza a
`2006-01-02T15:04:05Z`, la forma que el store escribe y compara como texto, y
un valor que no parsea aborta el import antes de escribir.

| Origen | Destino | Nota |
|---|---|---|
| `legacy.sessions` | `sessions` | `created_at = started_at`; `task_id`/`pinned_at` los llena la fase 2 |
| `legacy.observations` | `observations` | Todas salvo las 269 de control (abajo). Los soft-deletes se copian con su `deleted_at` |
| `legacy.user_prompts` | `user_prompts` | |
| `legacy.memory_relations` | `memory_relations` | Salvo las que tienen un extremo en un registro de control: ese extremo no se importa como observación y la FK contra `observations(sync_id)` no se puede cumplir. Se cuentan y se listan por `sync_id` |

Las tablas de sync y cloud no se copian: `sync_mutations` (27.755 filas, 21,7 MB
de los 64), `sync_state`, `sync_chunks`, `sync_enrolled_projects`,
`sync_apply_deferred`, `cloud_upgrade_state`, `prompt_tombstones`. Las columnas
`embedding*` y `expires_at` tampoco, porque no tienen una sola fila con valor.

El FTS no se copia: se reconstruye con `INSERT INTO observations_fts(observations_fts)
VALUES('rebuild')` al final, y lo mismo `prompts_fts`. Es la única forma de que
el índice quede consistente con los `rowid` copiados.

## Fase 2 — flujo

Los 331 registros del project `tasks` no entran como observaciones: su
contenido es el estado que las columnas van a guardar. Las excepciones son los
handoffs, que son memoria de verdad, y los registros que no tienen forma de
control (fila "otros" de la tabla).

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
| `task/<id>/progress/<uuid>/<slot>` | — | `progress_reports` | Fence ```json con `status, references, summary, next_steps`. `session_id=<uuid>`, `delegation_id=<slot>`, `role` = prefijo del slot (`explore`/`impl`/`review`), `refs` = `references`. Un `summary`/`next_steps` escrito como string o con ítems no string se guarda como array de strings. Upsert por `(session_id, delegation_id)` |
| otros | 9 | `observations` | Todo registro del project `tasks` sin forma de control, incluidas las filas sin `topic_key`, se importa como observación común: conserva project `tasks`, `type`, `title`, `content`, `sync_id` y timestamps, y su pie `**Task**` llena `task_id` como en la fase 3. El resumen los cuenta y los lista aparte. Decisión del usuario del 2026-10-19; antes se contaban y no se importaban |

Una tarea referida por un pin, un wt o un handoff sin registro `identity` se
crea con `title = key` y `status = todo`, y se lista al final como "sin
identity". Las keys de `topic_key` son 31; 28 tienen identity.

### Jerarquía

`parent_task_id` se arma al importar, en este orden, sobre el texto de
`**Product**` (decisión del usuario del 2026-10-20):

1. **Indicio explícito.** El resumen reconoce `parent <KEY>`, `padre <KEY>`,
   `subtarea de <KEY>`, `epic <KEY>` y `épica <KEY>` (no una key suelta entre
   paréntesis) y descarta el que nombra a la propia tarea. Si la key existe en
   el store, es el padre. Si no (`subtarea de 869eknv3x`, un id de ClickUp sin
   tarea), no hay padre por esa vía y el indicio se lista en el resumen.
2. **Regla de producto.** Sin indicio válido, la tabla `productParentRules`
   (`internal/legacy/parse.go`) asigna padre según el texto del producto. Hoy
   una sola regla: un producto que contiene `socios de negocio` cuelga de
   TRIPULSE-3945, que no es hija de sí misma. Cubre lo que el regex no capta:
   `TRIPULSE-3945 Socios de negocio` (key antes del texto) y las variantes sin
   key (`Integración de socios de negocio (integrator)`).
3. **Todo lo demás es épico** suelto dentro de `tripulse`.

Respeta el tope de 3 niveles de `specs/data-model.md`: un padre que dejaría a la
tarea en el nivel 4 o cerraría un ciclo se descarta y se lista en el resumen,
así los triggers no se disparan. El padre se escribe en un `UPDATE` posterior
al insert de las tareas, también en un reimport, que recalcula el árbol entero.

### Productos

`**Product**` es prosa libre: 28 identities traen 23 valores distintos (45
sobre la base de 2026-10-25) para lo que son cuatro productos. El import toma el
segmento anterior al primer ` · `, ` — `, ` / ` o ` (`; si es `ai-review`,
`dev-loop` o `engram-fork` (sin distinguir mayúsculas) ese es el producto, y cualquier otra
cosa va a `tripulse`. Decisión del usuario del 2026-10-20, con `dev-loop` como producto propio desde el 2026-10-25; antes se normalizaba
a 13 productos y la consolidación quedaba para después con el CLI.

La cadena original completa queda como primera línea de `tasks.notes`, con el
prefijo `[import-legacy] **Product** original:` para que se lea como
procedencia y no como una nota escrita por el usuario; el contenido libre de la
identity sigue debajo. El resumen imprime el mapeo cadena original → producto.

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
no existe en `tasks` se crea como arriba. `none` vale lo mismo que `ninguna`: es la key que
usan los pins `task/none/...`. Una key es un solo token sin espacios; lo demás
(`pendiente de decidir`) es irresoluble.

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
`tasks.key`, `worktrees.path`, `products.name`,
`progress_reports.(session_id, delegation_id)`. Correr dos veces da el mismo
resultado. `meta.last_import_at` y `meta.legacy_db_path` guardan la corrida, y
la transacción que aplica el DDL escribe `meta.schema_version`.

Reimportar vale sólo antes del corte (paso 4): pisa los campos importados
—`status`, `title`, producto y `notes` de cada tarea, y el content de las
observaciones legacy— con lo que diga el archivo legacy, así que un cambio
hecho con el CLI de neugram sobre esos campos se pierde. Lo que el legacy no
trae, como `parent_task_id`, no se toca.

La verificación corre dentro de la misma transacción que escribe, y cualquier
falla la revierte entera: la base destino queda como estaba, y si la corrida
la creó, se borra. `--db` apuntando al mismo archivo que `--engram-db` se
rechaza antes de abrir nada.

## Salida

`--dry-run` imprime el mismo resumen sin escribir:

```
sesiones      2.313
observaciones 6.536   (269 registros de control no importados como observación)
prompts       4.650
relaciones      353   (334 juzgadas, 19 pendientes)
tareas           31   28 con identity · 3 creadas desde pin/wt/handoff · N como épico
                      N con padre por indicio en **Product** · N por regla de producto
                      N indicios de padre a una key sin tarea, sin padre: <lista>
productos         4   (desde 23 cadenas; mapeo abajo)
pins            126   (101 con tarea, 25 none)
worktrees        82   (62 vivos, 20 muertos) · 1 registro sin path parseable
handoffs         62   vinculados a tarea y sesión
pies          1.195   1.059 con tarea · 136 ninguna · 1 sin key resoluble
tasks→obs         9   registros del project tasks sin forma de control, importados como observación: <lista>
hash          3.000   normalized_hash recomputado · 0 discrepancias
```

Con el snapshot del 2026-10-25 (89 tareas, 80 con identity): 4 productos
desde 45 cadenas (`tripulse` 77 tareas, `ai-review` 1, `dev-loop` 1, `engram-fork` 1, 9 sin
producto por no tener identity); 48 tareas con padre (23 por indicio, 25 por
la regla de socios de negocio; 42 cuelgan de TRIPULSE-3945) y 41 épicos; un
indicio sin tarea (TRIPULSE-3909 → 869eknv3x).

Medido de nuevo el 2026-10-19 sobre un snapshot de 8.571 observaciones. Las
diferencias son crecimiento de la base, no cambio de semántica: sesiones 2.492;
observaciones 7.455 (1.116 de control); prompts 5.839; relaciones 434 (415
juzgadas, 19 pendientes) más 14 omitidas por apuntar a registros de control;
tareas 80 (72 con identity, 8 creadas); productos 23 desde 40 cadenas; pins 277
(222 con tarea, 49 none, 6 reemplazados por un pin más nuevo de la misma
sesión); worktrees 159 (135 vivos, 24 muertos), 1 sin path parseable; handoffs
167; progress 367 desde 369 registros; pies 2.164 (2.002 con tarea, 161
ninguna, 1 sin key resoluble); otros 18 (importados como observación, abajo); hash 8.571 · 0 discrepancias;
timestamps 37.120 normalizados · 0 sin parsear. Con la decisión del mismo día
de importar los "otros" (12 con `topic_key`, 6 sin él): 18 observaciones más en
el project `tasks`, 931 registros de control no importados, pies 2.178 (2.013
con tarea, 164 ninguna, 1 sin key resoluble). Las 14 relaciones omitidas siguen
omitidas: cada una tiene el otro extremo en un registro `status`, `wt` o
`identity`, que no es observación.

## Ensayo previo (paso 3)

El paso 3 no corta. Se ensaya sobre un `CLAUDE_CONFIG_DIR` de ensayo (copia de
`settings.json` con el plugin de Engram deshabilitado y neugram cableado) y un
`NEUGRAM_HOME`/`NEUGRAM_ADDR` temporales, con una base importada de una copia
de `engram.db`. Nada se instala global y el daemon de Engram sigue vivo: el
import del ensayo no pisa nada real, y el import definitivo se repite justo
antes del corte, con el daemon de Engram apagado.

## Corte

El corte es la última entrega del paso 4 (`roadmap.md`), cuando están las 19
tools, las instrucciones editables y el gate de progreso. Después de un `import-legacy` real y un `neugram doctor` limpio, un solo edit a
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
