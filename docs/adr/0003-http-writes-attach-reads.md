# ADR 0003 — Escribir por HTTP, leer por ATTACH

Estado: superado por ADR 0011 · Origen: obs 6567

## Contexto

La capa necesita crear observaciones en Engram y leerlas junto con sus
columnas propias. Opciones: MCP-cliente-de-MCP, solo HTTP, o HTTP + acceso
directo al SQLite.

## Decisión

Escrituras hacia Engram solo por su HTTP: `POST /sessions`, `POST /observations`,
`PATCH /observations/{id}`, `POST /prompts`, `POST /sessions/{id}/end`,
`POST /conflicts/*`. Lecturas por `ATTACH DATABASE ~/.engram/engram.db` en modo
solo lectura, con `JOIN engram.observations ON sync_id`. La clave de unión es
`sync_id`, nunca `id`: sobrevive al cloud sync y a reimportar; `id` es un
autoincrement local.

## Consecuencias

- Filtros por tarea y por columna custom en una sola consulta SQL.
- `POST /observations` devuelve `id`, no `sync_id`: la capa resuelve el `sync_id` leyendo la fila por `id` en el ATTACH inmediatamente después (`specs/engram-provenance.md`, entonces `engram-boundary.md`).
- El HTTP de Engram no devuelve el envelope de conflictos (`judgment_required`); ese envelope lo produce su MCP. La capa lo reconstruye con `POST /conflicts/scan` tras guardar, o lo deja fuera de la primera versión. Ver `specs/api.md`.
- Descartado MCP-cliente-de-MCP porque los envelopes de las tools cambian más que las tablas. Descartado solo-HTTP porque los filtros salen en dos pasos y `/search` topea en 20.

---

Tras ADR 0011: no hay dos bases. Lo único que sobrevive es la elección de
`sync_id` como clave estable frente al `id` autoincrement, que ahora es la
clave pública de las observaciones propias.
