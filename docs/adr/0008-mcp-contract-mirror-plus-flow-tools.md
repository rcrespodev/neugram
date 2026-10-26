# ADR 0008 — MCP espejo de Engram más tools de flujo; schema solo por CLI

Estado: enmendado por ADR 0013 · Origen: obs 6575

## Contexto

Los skills `daily`, `handoff`, `review-wt` y la sección Engram del CLAUDE.md
dependen de los nombres `mem_*`. El `topic_key` como canal de control tiene un
riesgo documentado: mismo key, upsert silencioso, contenido anterior perdido.

## Decisión

El MCP de la capa espeja los nombres de Engram (`mem_save`, `mem_search`,
`mem_context`, `mem_update`, `mem_judge`, …) con parámetros nuevos
(`task_id`, campos custom, `where`) y agrega `task_pin`, `task_list`,
`task_status`, `worktree_register`, `handoff_save`. El control de flujo nunca
es un `mem_save` con `topic_key` mágico. Las tools de schema e instrucciones
son solo CLI: el agente no cambia sus propias reglas.

## Consecuencias

- Skills y CLAUDE.md migran casi sin tocar.
- Desaparece el upsert silencioso como riesgo de control.
- Un agente que quiera "arreglar" el protocolo tiene que pedirlo al usuario.

---

Tras ADR 0013: toda tool que escribe pide `session_id` explícito (sin
lectura del entorno) y las tools entran en dos tandas, 10 en el paso 3 y 9 en
el 4. Los nombres espejo, las tools de flujo y "schema solo por CLI" siguen
valiendo.
