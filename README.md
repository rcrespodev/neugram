# neugram

Capa de flujo de trabajo sobre [Engram](https://github.com/Gentleman-Programming/engram):
productos, tareas, sesiones y worktrees como tablas de primera clase, columnas
definidas por el usuario, instrucciones generadas desde el schema y gates que
un hook aplica de verdad. Engram sigue siendo la memoria; neugram es el eje que
Engram no tiene.

Un binario Go, un solo usuario, 100% local. No modifica Engram: escribe por su
HTTP y lee su SQLite con `ATTACH` de solo lectura.

## Documentación

| Doc | Qué responde |
|---|---|
| `docs/features.md` | Qué hace y qué no hace, con estado por feature |
| `docs/architecture.md` | Componentes, flujos, borde con Engram |
| `docs/domain.md` | Vocabulario: producto, tarea, pin, gate, handoff |
| `docs/adr/` | Por qué se decidió cada cosa; no reabrir sin nuevo ADR |
| `docs/specs/` | Contratos exactos: schema SQL, HTTP de Engram usado, tools MCP, CLI, hooks, import |
| `docs/roadmap.md` | Orden de construcción y qué verifica cada paso |

Las decisiones nacieron en un debate de diseño registrado en Engram
(project `engram-tasks`, observaciones 6562–6575, task `engram-fork-debat`).
