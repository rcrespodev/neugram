# ADR 0007 — Corte duro con `import-legacy`; lo viejo queda como historia

Estado: enmendado por ADR 0011 · Origen: obs 6574

## Contexto

Convivir con dos MCP rompe la premisa de escritor único: el agente podría
escribir por el `mem_save` de Engram saltando gates y campos required.

## Decisión

Un comando `import-legacy` lee el project `tasks` por HTTP y llena
`products`, `tasks`, `worktrees`, `sessions` y `observation_ext` (incluidos
los handoffs existentes, con `kind=handoff`). Después, un solo cambio en
`~/.claude/settings.json`: sale el MCP de Engram, entra `neugram mcp`; salen
`engram-tasks-session`, `engram-save-gate` y `engram-record-gate`, entran
`neugram hook <evento>`. Los registros viejos no se borran.

## Consecuencias

- El import es barato: `engram-record-gate` ya validó los formatos (`identity` con `**Name**: value`, `wt` con fence JSON).
- Hay un instante sin red de seguridad entre desactivar los hooks viejos y probar los nuevos. Se mitiga con `--dry-run` y `neugram doctor` antes del corte.

---

Tras ADR 0011: el corte duro sigue siendo un solo commit de config, y por la
misma razón. Cambia la fuente y el alcance del import: lee
`~/.engram/engram.db` directamente, no el HTTP, y trae toda la memoria
—observaciones, sesiones, prompts, relaciones—, no solo el project `tasks`.
