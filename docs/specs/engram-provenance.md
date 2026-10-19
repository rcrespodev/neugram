# Spec — Procedencia de Engram

Qué heredó neugram de [Engram](https://github.com/Gentleman-Programming/engram):
datos y schema, no código (ADR 0011). Medido el 2026-09-21 contra el checkout
local (`36c0819`, 2026-06-14), contra `origin/main` (`a2199d9`, 2026-09-14) y
contra `~/.engram/engram.db` (64 MB, 6.805 observaciones).

No es un contrato vivo: no hay nada del otro lado. Es el registro de qué entra
por el import, qué se le cambia al entrar, qué se verifica antes de escribir, y
qué se leyó de Engram sin copiarlo.

## Qué entra: datos y schema

Lo único que cruza de Engram a neugram son las filas de `~/.engram/engram.db` y
la forma de las cuatro tablas que las contienen. El importador lee el archivo,
no el código: `PRAGMA table_info` y un mapeo 1:1 de columnas. No hay payloads
codificados que haya que interpretar con la semántica del código viejo —los
únicos JSON de la base viven en `sync_mutations`, que se descarta entera.

| Tabla legacy | Filas | Destino |
|---|---|---|
| `observations` | 6.805 | `observations` (269 de control van a las tablas de flujo, ver `specs/import-legacy.md`) |
| `sessions` | 2.313 | `sessions` |
| `user_prompts` | 4.650 | `user_prompts` |
| `memory_relations` | 353 | `memory_relations` |

Hay una sola excepción a "el import no depende del código viejo":
`normalized_hash`. Es una columna cuyo valor lo produjo una función de Engram, y
neugram ya no hereda esa función. La semántica está reconstruida y verificada
—minúsculas, colapsar espacios, sha256; 3.000/3.000 filas reproducen el valor
almacenado, 0 discrepancias— y el gate del import la revalida sobre una muestra
antes de escribir (verificación 6, abajo).

## Qué se leyó como referencia

Leer no es copiar. Engram se leyó para medir el costo de absorberlo y para
confirmar tres cosas que después se escriben desde su fuente original:

| Qué | Dónde se leyó | Qué confirmó |
|---|---|---|
| Triggers FTS5 external-content | `internal/store/store.go:1012-1027` (observaciones) y `:1046-1061` (prompts) | Que Engram aplica el recetario canónico de `https://www.sqlite.org/fts5.html#external_content_tables` tal cual, con sus columnas. La fuente de la copia textual es la doc de SQLite; el DDL vive en `specs/data-model.md` |
| `normalized_hash` | La base viva, no el código: 3.000 filas recomputadas | Que son tres renglones reproducibles, no una caja negra |
| Reintento ante `SQLITE_BUSY` / `SQLITE_LOCKED` | `internal/store/store.go` | Que los códigos a reintentar son 5 y 6. Es un hecho de SQLite, no una invención de Engram |
| Forma del schema y del dato | `~/.engram/engram.db` | Lo que documentan las dos secciones de abajo |

## Qué se decidió no reusar, y por qué

La decisión de escribir el store en vez de copiarlo y podarlo está en ADR 0011.
El detalle medido que la sostiene, para que no haya que volver a medirlo:

| Qué se descartó | Tamaño | Motivo medido |
|---|---|---|
| `internal/store/store.go` | 7.038 líneas, 183 funcs | 49% del cuerpo es sync y cloud; 29% no lo usa ninguna tool ni ruta de `specs/api.md`. Del 22% restante, el 100% cambia de firma por `id`→`sync_id` |
| `internal/store/relations.go` | 1.507 líneas | `JudgeBySemantic` y la mitad semántica de `ScanProject` dependen de `internal/llm`, que ADR 0008 deja afuera; `GetRelationByIntID` desaparece con `id`→`sync_id`. El núcleo de `FindCandidates` son ~40 líneas de lógica |
| `internal/store/diagnostic.go` | 11 KB | `neugram doctor` chequea un schema distinto |
| Tests del paquete `store` | 14.411 líneas, 279 funcs | El 61% referencia símbolos de sync, cloud o export y muere con la poda; el resto no compila tras el cambio de clave. Red heredada: cero |
| `internal/project`, `internal/timeutil` | 582 + 49 líneas | Autocontenidos y con tests propios, pero copiarlos reintroduce copyright ajeno por ~630 líneas de lógica sin dificultad: detección git remote → raíz git → basename, y formato ISO 8601 UTC. Se escriben en `internal/project` e `internal/timeutil` |
| `internal/cloud/**`, `internal/sync/**` | 1,5 MB | Engram Cloud y su sync. ADR 0005 |
| `internal/mcp/`, `internal/server/` | 158 KB | neugram define su propio MCP y sus propias rutas (ADR 0008, `specs/api.md`) |
| `internal/setup/`, `internal/tui/`, `internal/obsidian/` | 452 KB | Instalador del plugin, TUI Bubbletea y export a Obsidian. Fuera de alcance (ADR 0005) |
| `internal/llm/` | 72 KB | El juicio semántico lo hace el agente por `mem_judge` (ADR 0008) |

## Relación con upstream

Corte definitivo: no se re-sincroniza, y sin código compartido no hay nada que
re-sincronizar. Entre `36c0819` y `a2199d9` hay 481 commits en tres meses (12 en
junio, 40 en julio, 236 en agosto, 194 en septiembre hasta el día 14). Ese ritmo
es lo que hacía inviable sostener un árbol heredado, y es el costo que ADR 0001
quiso evitar.

Un arreglo puntual de upstream se lee y se reimplementa. No se mergea.

## Licencia y atribución

Engram es MIT, `Copyright (c) 2026 Alan Buscaglia`. neugram es MIT,
`Copyright (c) 2026 Rodrigo Crespo`.

MIT exige que el aviso de copyright viaje con "todas las copias o porciones
sustanciales del Software". Sin código copiado esa obligación no se activa: el
copyright cubre la expresión, no las ideas, y leer para aprender no es copiar.
No hay `NOTICE`, no hay copia del LICENSE de Engram en el árbol y ningún
archivo lleva encabezado de atribución.

Queda un crédito de cortesía en el `README.md`, porque el diseño deriva de
Engram y decirlo es honesto.

**La condición es explícita y vale durante toda la implementación**: si se copia
de Engram aunque sea un fragmento de código, la obligación vuelve. Ese archivo
lleva encabezado de atribución —origen upstream, copyright de Alan Buscaglia,
aviso MIT— y reaparece el `NOTICE` en la raíz con el texto MIT de Engram
verbatim. El disparador es que se haya copiado código, no que se haya leído.

## Schema heredado y qué se le cambió

El schema de partida es el que produjo el Engram que corre, leído de la base
viva, no el del checkout. `specs/data-model.md` tiene el DDL final, escrito por
neugram. Los cambios respecto del legacy, cada uno verificado contra los datos:

| Cambio | Evidencia |
|---|---|
| `observations.sync_id` pasa de `TEXT` indexado a `NOT NULL UNIQUE` | 6.805 filas, 0 nulas, 0 duplicadas |
| `observations.project` pasa a `NOT NULL` | 0 filas con project nulo |
| `observations.scope` gana `CHECK (scope IN ('project','personal'))` | únicos valores presentes: 6.784 `project`, 21 `personal` |
| `observations` absorbe `task_id` y `kind` | ADR 0011 |
| `sessions` absorbe `task_id`, `pinned_at`, `pinned_by` | ADR 0011 |
| Se eliminan `embedding`, `embedding_model`, `embedding_created_at` | 0 filas con embedding |
| Se elimina `expires_at` | 0 filas con valor |
| Se eliminan `sync_state`, `sync_mutations`, `sync_chunks`, `sync_enrolled_projects`, `sync_apply_deferred`, `cloud_upgrade_state` | sin cloud no tienen lector; `sync_mutations` sola son 27.755 filas y 21,7 MB de los 64 |
| Se elimina `prompt_tombstones` | existía para propagar borrados al cloud; 6 filas |
| `memory_relations.source_id`/`target_id` ganan FK contra `observations(sync_id)` | 0 filas colgadas |

Lo que conserva la forma del legacy, porque el dato importado depende de ella:
`observations_fts` y `prompts_fts` como FTS5 external content sobre
`content_rowid='id'` con sus tres triggers cada una, y las columnas de dedupe
(`normalized_hash`, `revision_count`, `duplicate_count`, `last_seen_at`) y de
revisión (`review_after`, 1.155 filas con valor).

## Verificación al importar

`import-legacy` aborta si alguna no se cumple (`specs/import-legacy.md`):

1. `~/.engram/engram.db` abre en solo lectura y tiene `observations`, `sessions`, `user_prompts`, `memory_relations` con las columnas de arriba (`PRAGMA table_info`).
2. Ninguna observación ni prompt con `sync_id` nulo o duplicado.
3. Ninguna observación ni prompt con `session_id` sin fila en `sessions`.
4. Ninguna relación con `source_id` o `target_id` inexistente.
5. Todo `scope` está en el enum; todo `project` es no nulo.
6. `normalized_hash` recomputado sobre una muestra de observaciones coincide con el almacenado, en todas.

Las seis dan verde hoy; la 6, 3.000/3.000. Un fallo de las cinco primeras no es
un bug del import: es un dato que la base legacy tiene y el schema propio no
admite, y se decide caso por caso. Un fallo de la 6 sí es un bug del import —
significa que la reconstrucción del hash no reproduce la semántica que produjo
los datos, y hasta arreglarla el dedupe de las observaciones importadas queda
roto en silencio.

## Lo que la absorción hace desaparecer

- El daemon de Engram en `:7437` y sus 37 rutas HTTP. Queda un solo puerto, `:7438`.
- El `ATTACH DATABASE ... mode=ro` de operación, la vista `v_observations` y la tabla `observation_ext`. Sobrevive un ATTACH de importación, que vive lo que dura el comando.
- La deuda del payload de `POST /conflicts/scan`: los candidatos salen de `FindCandidates`, que es código propio.
- La deuda del parámetro de `GET /project/current`: la detección es `internal/project`, código propio.
- El error `session_project_mismatch` como borde: sigue siendo una regla, pero propia y verificable en la misma transacción.
