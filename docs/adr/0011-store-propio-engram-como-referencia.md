# ADR 0011 — neugram escribe su propio store; Engram se lee, no se copia

Estado: aceptado · Origen: obs 6807, 6808 (project `neugram`)

## Contexto

ADR 0001 eligió capa sobre fork con un argumento: mantener parches sobre un
upstream rápido cuesta más que convivir con un borde. El borde resultó caro por
su cuenta: dos bases, dos daemons, dos puertos, una tabla 1:1 unida por
`sync_id`, un ATTACH de solo lectura, vistas TEMP que hay que recrear en cada
arranque y un contrato implícito sobre `engram.observations` que `doctor` tiene
que revalidar. Ninguna de esas piezas entrega valor: sostienen la frontera.

La primera respuesta fue copiar. Engram es MIT y es Go, el mismo lenguaje, y su
núcleo de memoria parecía entrar entero como paquete heredado, con una poda de
sync y cloud. Medir qué entraba de verdad desarmó esa premisa:

- De las 6.025 líneas de función de `internal/store/store.go`, el 49% (2.928) es
  sync y cloud, y el 29% (1.734) no lo usa ninguna de las 19 tools ni de las 42
  rutas de `specs/api.md`. Queda el 22% —1.360 líneas— y las 1.360 cambian de
  firma: `specs/api.md` usa `sync_id` como clave pública y `specs/data-model.md`
  no expone `observations.id`, mientras las heredadas toman `id int64`.
- La red de tests heredada tras la poda es cero, que era el argumento fuerte de
  la cirugía. De las 14.411 líneas de test del paquete, el 61% referencia
  símbolos de sync, cloud o export y muere con la poda; lo que sobrevive está
  escrito contra `id int64` y no compila después del cambio de clave.
- Nada de lo "algorítmicamente difícil" resiste la medición. `hashNormalized`
  son tres renglones; los triggers FTS5 son el recetario canónico de la
  documentación de SQLite; el núcleo de `FindCandidates` son ~40 líneas de
  lógica sin embeddings ni matemática de similitud.

Contra eso, escribir el store entero a mano cuesta unas 250 líneas más que la
variante híbrida —copiar ~900 líneas con atribución— y ahorra leer ~8.000
líneas de código ajeno para decidir qué se borra.

## Decisión

neugram escribe su propio store, desde cero, contra `specs/data-model.md`. Cero
código Go copiado de Engram: se lee como referencia, no se usa como cantera.

Lo que no cambia respecto de la decisión original: neugram es dueño único de una
sola base (`~/.neugram/neugram.db`), un binario, un proceso y un puerto
(`:7438`). Observaciones, sesiones, prompts, FTS y detección de conflictos son
tablas propias. `~/.engram/engram.db` (64 MB) se importa una vez y queda como
archivo histórico, no como dependencia viva. El corte con upstream es
definitivo: no se re-sincroniza.

Lo que cambia es el método. No hay paquete heredado: no hay árbol que podar ni
código con copyright ajeno en el repo. La detección de repo y el formato de
timestamps —lo único que se iba a copiar como paquete entero— se escriben en
`internal/project` e `internal/timeutil`.

Dos piezas se escriben desde su fuente original, que no es Engram:

- Los triggers FTS5 de tabla external-content son el recetario de
  `https://www.sqlite.org/fts5.html#external_content_tables`: la fila con el
  comando `'delete'` en el `AFTER DELETE` y en el `AFTER UPDATE`. Engram los usa
  igual, con sus columnas, en `internal/store/store.go:1012-1027`; eso confirma
  la receta, no la origina. Van verbatim en `specs/data-model.md`, que es donde
  una copia textual sí es obligatoria: un trigger mal escrito corrompe el índice
  en silencio en vez de romper ruidosamente.
- `normalized_hash` es minúsculas, colapsar espacios, sha256. Verificado dos
  veces de forma independiente contra la base viva: 3.000/3.000 filas
  reproducen el hash almacenado, 0 discrepancias.

## Consecuencias

- Caen `observation_ext`, el ATTACH, `v_observations` y el borde HTTP con `:7437`. `task_id` y `kind` son columnas de `observations`; la sesión es una sola fila.
- Caen dos deudas del roadmap: el payload de `POST /conflicts/scan` y el parámetro de `GET /project/current`. El código que las resolvía es ahora propio.
- `sync_id` sobrevive al cloud que lo nombró: es la clave de unión de los 6.805 registros que se importan y la clave pública de la API. `id` se conserva porque FTS5 lo exige como `content_rowid`.
- El costo que ADR 0001 evitaba —mantener código heredado— no aparece, porque no se hereda código. Se paga en escritura: el store propio y sus tests, contra `specs/data-model.md`. La versión anterior de este ADR decía que "del `internal/` de Engram entra cerca del 8%"; ese 8% estaba medido al granulado equivocado, sobre el directorio de upstream y no sobre lo aprovechable. Las cifras reales son otras dos: sólo el 22% de `store.go` es lo que neugram necesita, y de ese 22% el 100% cambia de firma por `id`→`sync_id`.
- Sin código copiado, la obligación del MIT no se activa: el copyright cubre la expresión, no las ideas, y leer para aprender no es copiar. No hay copia del LICENSE de Engram en el árbol, ni `NOTICE` en la raíz, ni encabezados de atribución por archivo. Queda un crédito de cortesía en el `README.md`: el diseño deriva de Engram y decirlo es honesto.
- **Regla, no nota al pie**: si durante la implementación se copia de Engram aunque sea un fragmento de código, la obligación vuelve para ese archivo —encabezado de atribución con el copyright de Alan Buscaglia y el aviso MIT— y con ella el `NOTICE` en la raíz. El disparador es que se haya copiado código, no que se haya leído.
- La comparación entre podar, escribir a mano y el híbrido está medida y cerrada. No se reabre sin un ADR nuevo y una medición nueva: las cifras de arriba son el registro de por qué se eligió escribir.
