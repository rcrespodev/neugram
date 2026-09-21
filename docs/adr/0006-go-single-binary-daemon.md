# ADR 0006 — Go, un binario, daemon más clientes finos

Estado: aceptado · Origen: obs 6573

## Contexto

Los hooks corren en cada prompt y en cada fin de turno. Hoy son 1.521 líneas
de Python y bash que arrancan un intérprete cada vez. Hooks y MCP no pueden
competir por el archivo SQLite.

## Decisión

Un binario Go con subcomandos: `serve` (daemon con la única conexión SQLite,
hace el ATTACH, sirve HTTP y UI, habla con Engram), `mcp` (stdio, cliente fino
del HTTP propio), `hook <evento>` (levanta el daemon si no corre), y el CLI de
operación. SQLite vía modernc.org/sqlite, sin cgo.

## Consecuencias

- Arranque en milisegundos, sin node_modules ni binarios nativos.
- Un solo escritor sobre la BD propia.
- El patrón es el mismo que usa Engram; su código es referencia legible.
