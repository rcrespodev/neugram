# neugram

Memoria y flujo de trabajo para Claude Code en un solo binario: productos,
tareas, sesiones y worktrees como tablas de primera clase, observaciones con
tarea, columnas definidas por el usuario, instrucciones generadas desde el
schema y gates que un hook aplica de verdad.

El diseño del núcleo de memoria —observaciones, sesiones, prompts, FTS5 y
detección de conflictos— deriva de
[Engram](https://github.com/Gentleman-Programming/engram)
(MIT, © 2026 Alan Buscaglia), al que neugram reemplaza y del que importa su
base entera. El código es propio: no se copió nada (ADR 0011). Qué se leyó como
referencia, qué entra por el import y qué pasa con la licencia si algún día se
copia un fragmento está en `docs/specs/engram-provenance.md`.

Un binario Go, un usuario, una base (`~/.neugram/neugram.db`), un puerto, 100%
local. Sin sync, sin nube.

## Documentación

| Doc | Qué responde |
|---|---|
| `docs/features.md` | Qué hace y qué no hace, con estado por feature |
| `docs/architecture.md` | Componentes, paquetes, flujos y qué no es |
| `docs/domain.md` | Vocabulario: producto, tarea, pin, gate, handoff |
| `docs/adr/` | Por qué se decidió cada cosa; no reabrir sin nuevo ADR |
| `docs/specs/` | Contratos exactos: API (tools MCP y HTTP), schema SQL, procedencia de Engram, CLI, hooks, import |
| `docs/roadmap.md` | Orden de construcción y qué verifica cada paso |

Las decisiones nacieron en un debate de diseño registrado en Engram
(project `engram-tasks`, observaciones 6562–6575, task `engram-fork-debat`).
