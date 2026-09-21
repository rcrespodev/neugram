# ADR 0009 — Instrucciones generadas + libres; gates fijos con interruptor; prompts por hook

Estado: aceptado · Origen: obs 6572

## Contexto

Hoy el protocolo vive en el CLAUDE.md y el hook `Stop` aplica tres bloqueos
codificados en Python. `mem_save_prompt` depende de que el modelo se acuerde en
cada turno, incluso después de una compactación.

## Decisión

Las instrucciones que inyecta SessionStart se componen de una parte generada
desde `schema_fields` y una parte libre editable por CLI, guardada en la BD,
global con override por producto. Los gates son un set fijo, cada uno código
Go más un flag: sin DSL. La captura de prompts la hace el hook
`UserPromptSubmit`, no el agente.

## Consecuencias

- Un DSL de reglas reemplazaría 40 líneas de Go que el propio usuario compila. Un gate que hace falta dos veces entra al set fijo.
- La sección Engram del CLAUDE.md migra a la parte libre y deja de duplicarse por proyecto.
- Ningún prompt se pierde por olvido del modelo.
