# ADR 0004 — Columnas custom con DDL real, definidas en `schema_fields`

Estado: aceptado · Origen: obs 6565

## Contexto

La rigidez de columnas de Engram fue lo que provocó los parches del flujo
actual. La capa debe permitir agregar metadata sin tocar código.

## Decisión

Una tabla `schema_fields` (tabla destino, nombre, tipo, required, enum,
descripción, índice) es la fuente única. De ahí salen: DDL idempotente
(`ALTER TABLE ... ADD COLUMN`) al arrancar el daemon, JSON Schema de las tools
MCP, el texto generado de las instrucciones y la validación de `mem_save` y
del hook `Stop`. Tipos acotados a lo que SQLite modela. Los campos custom no
entran al FTS: solo filtros SQL. `schema.yaml` es export/import para versionar,
no verdad.

## Consecuencias

- Filtros SQL directos y UI sin código especial por campo.
- `DROP COLUMN` exige SQLite ≥ 3.35; modernc.org/sqlite lo cubre.
- Descartado JSON + generated columns (dos lugares) y EAV (un join por campo).
- La BD es la verdad porque el binario debe arrancar sin depender de un archivo externo.
