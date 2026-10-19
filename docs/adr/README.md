# ADRs

Una decisión por archivo. Estado `aceptado` salvo que otro ADR lo reemplace.
Cada ADR cita la observación de Engram donde se tomó (project `engram-tasks`).
Reabrir una decisión es escribir un ADR nuevo que la reemplace, no editar esta.

Un ADR alcanzado por uno posterior no se reescribe: cambia su `Estado:` a
`superado por ADR NNNN` o `enmendado por ADR NNNN` y cierra con una nota al pie
que dice qué sigue valiendo y qué no.

| ADR | Decisión | Estado |
|---|---|---|
| 0001 | Capa sobre Engram, no fork | superado por 0011 |
| 0002 | Eje Producto → Tarea → Observación con tablas propias | aceptado |
| 0003 | Escribir por HTTP, leer por ATTACH | superado por 0011 |
| 0004 | Columnas custom con DDL real desde `schema_fields` | enmendado por 0011 |
| 0005 | Un usuario, local, UI web propia; Engram Cloud se apaga | enmendado por 0011 |
| 0006 | Go, un binario, daemon más clientes finos | enmendado por 0011 |
| 0007 | Corte duro con `import-legacy` | enmendado por 0011 |
| 0008 | MCP espejo más tools de flujo; schema solo por CLI | aceptado |
| 0009 | Instrucciones generadas + libres; gates fijos; prompts por hook | aceptado |
| 0010 | Reportes de progreso: tabla propia, tool y gate | aceptado |
| 0011 | neugram escribe su propio store; Engram se lee, no se copia | aceptado |
| 0012 | Jerarquía de tareas con `parent_task_id` | aceptado |
