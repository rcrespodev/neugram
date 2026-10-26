# ADR 0013 — `session_id` explícito en las tools; el corte pasa al paso 4

Estado: aceptado · Origen: decisión del usuario del 2026-10-26 (task `engram-fork-debat`)

## Contexto

El ADR 0008 dejó que el MCP supiera la sesión por su cuenta. La sesión sale de
`CLAUDE_CODE_SESSION_ID`, pero el proceso MCP sobrevive a `/clear`: el id del
entorno queda viejo y las escrituras caen en la sesión anterior, con su pin.
Además, el roadmap cortaba (ADR 0007) al final del paso 3, con solo 10 de las
19 tools, y tras el corte no queda otro MCP: el CLAUDE.md y los skills usan
`mem_context`, `mem_review` y el resto.

## Decisión

- `session_id` es un argumento explícito y obligatorio de toda tool MCP que escribe, y opcional en las lecturas. El MCP no lee el entorno y no tiene fallback. El hook `session-start` imprime `CLAUDE_CODE_SESSION_ID=<id>` como primera línea en todo `source`, incluido `fork`.
- Una escritura con un `session_id` sin fila falla con `unknown_session`; el MCP no crea sesiones.
- El corte pasa al paso 4, junto con las 9 tools restantes y las instrucciones editables. El paso 3 entrega 10 tools y los hooks, y se verifica con un test e2e y un ensayo aislado (`CLAUDE_CONFIG_DIR` de ensayo); nada se instala global y Engram sigue vivo.

## Consecuencias

- Una sesión nueva tras `/clear` escribe en su propia fila; se descarta el mapa `pid → sesión` y el header `X-Neugram-Claude-Pid`.
- Cada tool que escribe gana un parámetro, y el agente depende de leer la primera línea que imprime el hook. Si el hook no corrió, la escritura falla con un `hint` en vez de caer en una sesión ajena.
- `block-topic-key-collision` arranca apagado hasta el paso 4: con el gate activo, el protocolo vigente de `topic_key` sería todo 409.
- Hay dos pasos con el MCP a medias, pero el flujo actual no se toca hasta que están todas las tools.

---

Reemplaza en el ADR 0008 la resolución implícita de la sesión y el momento del
corte; el espejo de nombres, las tools de flujo y "schema solo por CLI" siguen
valiendo. Del ADR 0007 solo cambia cuándo ocurre el corte.
