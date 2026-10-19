# ADR 0001 — Capa sobre Engram, no fork

Estado: superado por ADR 0011 · Origen: obs 6562, 6567

## Contexto

Engram (MIT, ~100 commits/mes, rc cada dos días) no tiene tarea ni producto en
su modelo. Todo el flujo actual (pie `**Task**`, project `tasks` como tabla de
control, `identity`/`status`/`wt`/`pin` en `topic_key`) es un workaround. El
checkout local está 481 commits detrás con 9 archivos sin commitear.

## Decisión

No forkear ni parchear upstream. Construir una capa aparte con su propio SQLite
y su propio MCP, que usa Engram sin modificarlo: escribe por su HTTP en
`127.0.0.1:7437`, lee su base con `ATTACH` de solo lectura. Las observaciones
siguen viviendo en Engram. Upstream es referencia legible, no dependencia de
código: sus paquetes están bajo `internal/` y no son importables.

## Consecuencias

- Cero mantenimiento de parches sobre un upstream rápido.
- El schema de `observations` es un contrato implícito. Verificado estable en 481 commits (2026-06 → 2026-09); `specs/engram-provenance.md` (entonces `engram-boundary.md`) lista qué se asume y cómo detectar rotura.
- El cloud sync de Engram solo cubre la mitad Engram. Aceptado: ver ADR 0005.

---

Tras ADR 0011: el diagnóstico sigue valiendo — Engram no tiene tarea ni
producto, y el flujo sobre `topic_key` era un workaround. La decisión no:
no hay capa, ni HTTP, ni ATTACH, y tampoco hay código heredado. neugram
escribe su propio store y es dueño de la base; Engram se lee como referencia.
