# Spec — Hooks

`neugram hook <evento>` lee el JSON que Claude Code manda por stdin, llama al
daemon en `:7438` e imprime lo que Claude espera. Cada hook termina en menos
de 200 ms cuando el daemon ya corre.

## Ciclo de vida del daemon

Lo comparten los hooks, `neugram mcp` y el CLI de operación, a través de un
cliente HTTP único (`internal/client`):

```text
ensureDaemon()
  GET /health
    responde y version == la del cliente → listo
    responde con otra version → política de versión (abajo)
    no responde → lanza `neugram serve` desatachado (setsid, stdio a ~/.neugram/serve.log)
                  espera hasta 2 s a /health
```

- El puerto es el lock. `serve` hace el bind de `127.0.0.1:7438` antes de abrir la base; si el bind falla porque otro `serve` ganó la carrera, sale con 0 sin tocar nada.
- Al apagarse (`/shutdown` o señal) `serve` cierra la base —checkpoint del WAL— y recién después suelta el puerto, así el reemplazo nunca abre la base con el viejo aún escribiendo.
- El chequeo de versión existe porque el daemon queda residente: después de `make install` el binario nuevo reemplaza al viejo en la primera llamada de un hook o del CLI.

### Política de versión

- **Hooks y CLI reemplazan** a un daemon de otra versión: `POST /shutdown` y arranque del propio.
- **El MCP nunca reemplaza a un daemon vivo.** Solo lo arranca si no responde; si la versión difiere, deja un aviso en `hooks.log`. El MCP es residente: tras `make install` su binario viejo vería una versión distinta y haría `/shutdown` del daemon que acaban de levantar los hooks, en bucle.
- **Un build `-wt-`** (hecho desde un worktree) no reemplaza a un daemon que no sea `-wt-`, salvo que `NEUGRAM_ADDR` esté seteado explícitamente: protege la base real de los binarios de prueba.

### Fail-open

Presupuesto de 2 s por llamada al daemon. Si no responde, el hook escribe el
motivo (`daemon_unreachable`) en stderr y en `hooks.log`, sale con 0 y no
imprime nada. neugram caído nunca bloquea un turno ni una sesión; `neugram
doctor` cuenta los `daemon_unreachable` del log.

Consecuencias aceptadas: con el daemon caído `stop` no protege (el gate no
corre) y `user-prompt-submit` pierde el prompt, que solo se loguea; no hay
cola de reenvío.

Los cinco eventos reemplazan a `engram-tasks-session` (SessionStart),
`engram-save-gate` (Stop), `engram-record-gate` (PreToolUse sobre `mem_save`)
y a los hooks del plugin de Engram. En el paso 3 se ensayan en un
`CLAUDE_CONFIG_DIR` aislado con `NEUGRAM_HOME`/`NEUGRAM_ADDR` temporales; no se
cablean en el `~/.claude/settings.json` real hasta el corte del paso 4
(`roadmap.md`).

## Cableado en `~/.claude/settings.json`

```json
"SessionStart":      [{ "matcher": "startup|resume|clear|compact|fork", "hooks": [{ "type": "command", "command": "neugram hook session-start", "timeout": 10 }] }],
"UserPromptSubmit":  [{ "hooks": [{ "type": "command", "command": "neugram hook user-prompt-submit", "timeout": 5 }] }],
"Stop":              [{ "hooks": [{ "type": "command", "command": "neugram hook stop", "timeout": 10 }] }],
"SubagentStop":      [{ "hooks": [{ "type": "command", "command": "neugram hook subagent-stop", "timeout": 10 }] }],
"SessionEnd":        [{ "hooks": [{ "type": "command", "command": "neugram hook session-end", "timeout": 5 }] }]
```

## `session-start`

Entrada: `session_id`, `cwd`, `source` (`startup|resume|clear|compact|fork`).

Es idempotente: lo único que escribe es la fila de la sesión.

1. Upsert local de `sessions(id, project, directory, started_at)` si la fila no existe; `project` sale de `internal/project` (derivado del git remote o del directorio). En `resume`/`compact` la fila ya existe y conserva su pin.
2. Salida a stdout, en este orden:
   - `CLAUDE_CODE_SESSION_ID=<id>`: **primera línea, en todo `source`**. La sesión no puede leer su propio id y las tools MCP lo piden como argumento (`specs/api.md#convenciones`); el hook es la única fuente. Tras `/clear` el id es nuevo, así que la línea se vuelve a imprimir.
   - Instrucciones: parte generada + parte libre (`specs/instructions-and-gates.md`), con override del producto de la tarea pineada si la hay. En el paso 3 solo la generada.
   - Estado del pin: `Pin: <key> — <título> [<status>]`, `Pin: none`, o `Sin pin todavía. Fijalo con task_pin antes de tocar archivos.` seguido de las tareas abiertas (`status != done`) con producto y status.
   - Último handoff de la tarea pineada, content entero. Si no hay pin, el handoff más reciente cuya sesión tenía el mismo `cwd`, marcado como pista.
   - Worktrees registrados de la tarea pineada con vivo/muerto.

En `clear` la sesión nueva no hereda pin: sale sin pin y con la pista del
último pin o handoff del mismo `cwd`.

## `user-prompt-submit`

Entrada: `session_id`, `prompt`.

`POST /prompts {session_id, content}` al daemon (`specs/api.md`). Se omite si
el prompt tiene menos de 10 caracteres sin espacios. No imprime nada. Un fallo
no bloquea el prompt: se loguea.

## `stop`

Entrada: `session_id`, `transcript_path`, `stop_hook_active`.

Si `stop_hook_active` es `true` termina con exit 0 (evita el bucle). Si no:

1. El daemon lee el transcript **en reversa**, desde el final hasta el último mensaje humano, para que el costo no dependa del largo de la sesión (los `.jsonl` pasan de 3,6 MB en p95), y extrae: herramientas `Edit`/`Write`/`MultiEdit`/`NotebookEdit` con sus paths; `Bash` mutante según la [heurística de mutación](#heurística-de-mutación); llamadas a `mem_save`/`mem_update`/`handoff_save` de neugram; llamadas a `worktree_register` y `progress_write`.
2. Aplica cada gate habilitado (`specs/instructions-and-gates.md`).
3. Si alguno falla, imprime JSON `{"decision":"block","reason":"<markdown>"}` y sale con 0. El `reason` nombra el gate, lo que vio, y la llamada exacta que lo destraba (por ejemplo `task_pin {"task_id":"TRIPULSE-3652"}` con las candidatas abiertas). Si todos pasan, sale con 0 sin salida.

**Borde del turno.** Es todo lo que despierta al agente, no solo el mensaje
humano: un mensaje humano real, `task-notification` (por `origin.kind` o por el
prefijo `<task-notification>`), `<teammate-message`, "Another Claude session
sent a message" y el texto `[Request interrupted by user…`. Motivo: `stop` corre
una vez por despertar y el gate evalúa lo que pasó desde la última vez que el
agente fue despertado, igual que el gate legacy. No son borde los `tool_result`,
las entradas `isMeta` que no son despertares (feedback de hooks, `system-reminder`,
`auto-continuation`) ni la salida de comandos locales (`<local-command-…`,
`<bash-stdout>`).

Los subagentes heredan `session_id`, así que heredan el pin; los gates de pin
nunca les piden un pin propio. El `stop` del padre no cuenta las mutaciones de
los subagentes.

### Heurística de mutación

Es la del gate legacy (`~/.claude/hooks/engram-save-gate` y su `.test.py`,
cuyos casos se portan como tests) más la lista siguiente. Un turno **mutó** si
tiene alguna de estas llamadas con `tool_result` sin `is_error`:

- `Edit`, `Write`, `MultiEdit`, `NotebookEdit` sobre un path no scratch.
- `Bash` con `git commit`, `git push`, `gh pr create`, `gh issue create`: durables sin importar el path.
- `Bash` con una redirección `>`/`>>` a un archivo (no `2>`, `&>`, `>&2` ni `/dev/null`), `sed -i`, `tee`, `mv`, `rm`, `mkdir`, `touch`, `go mod`, `npm i`: mutan según el path que nombran. El cuerpo de un heredoc es dato, no sintaxis.
- `git worktree add` y `EnterWorktree`: además disparan `require-worktree-registered`. Un path relativo de `git worktree add` se resuelve con el campo `cwd` de la entrada del transcript donde aparece.

Se ignoran los paths bajo `/scratchpad/`, `/tmp/`, `/.git/` y `/node_modules/`:
es trabajo descartable, no un entregable.

Las tools de neugram se cuentan por el **nombre exacto** con el prefijo del
servidor, `mcp__neugram__mem_save` y compañía; no por sufijo, para que
`mcp__plugin_engram_engram__mem_save` no satisfaga el gate durante la
convivencia con Engram, ni `mem_save_prompt` a `mem_save`. Cuentan solo las
llamadas cuyo `tool_result` no fue error.

## `subagent-stop`

Entrada: `session_id`, `transcript_path` (el del padre), `agent_id`,
`agent_type`, `agent_transcript_path` (el del subagente), `stop_hook_active`.

Misma lógica que `stop`, con diferencias: no aplica `require-save-on-mutation`
(el subagente reporta al padre, que guarda); aplica `require-progress-report`,
que solo existe acá; y escribe en el log qué subagente mutó qué.

El transcript que se escanea es `agent_transcript_path`, no el del padre, y se
lee **entero**: un subagente no tiene mensajes humanos que marquen el inicio
del turno (su primera entrada `user` es el prompt de la delegación) y son
chicos. Un bloqueo le devuelve el control al subagente una sola vez; con
`stop_hook_active` el segundo intento termina igual.

**Alcance en el paso 3:** aplica solo `require-task-pin-on-mutation` y
`require-worktree-registered`. `require-progress-report` entra en el paso 4,
con `progress_write`. El `stop` del padre no consume lo que el subagente
mutó (paridad con el gate legacy); `subagent-stop` solo lo loguea.

## `session-end`

Entrada: `session_id`, `reason`.

`POST /sessions/{id}/end` al daemon y `sessions.ended_at = now`. No imprime.

## Registro

Cada hook agrega una línea a `~/.neugram/hooks.log`: evento, sesión, duración,
gates evaluados y resultado. Es lo que se mira cuando "el hook bloqueó y no sé
por qué".

## Hallazgos del spike de Claude Code

Verificados el 2026-10-26 contra la documentación oficial de hooks
(`code.claude.com/docs/en/hooks`), el changelog local y 1.087 transcripts de
`~/.claude*/projects`. No hay volcados de hooks reales en `~/.claude*/` (los
hooks locales no guardan su stdin), así que los campos de payload salen de la
documentación y quedan por confirmar en el ensayo aislado del paso 3.

| Supuesto | Hallazgo | Consecuencia |
|---|---|---|
| `SubagentStop` identifica el transcript del subagente | Confirmado en la doc: trae `agent_id`, `agent_type`, `agent_transcript_path` (campos agregados en Claude Code 2.0.42) y `last_assistant_message`; `transcript_path` sigue siendo el del padre. El archivo existe en disco: `<proyecto>/<session-id>/subagents/agent-<agent_id>.jsonl`, con `isSidechain: true` y `agentId` | Se cierra la deuda del roadmap sobre `require-progress-report`; el hook manda `agent_transcript_path` al daemon |
| Cada entrada del transcript trae `cwd` | Parcial: lo traen todas las entradas de conversación (`user`, `assistant`, `attachment`, `system`: 2.293 de 2.293 en la muestra) y ninguna de las de metadata (`mode`, `permission-mode`, `last-prompt`, `ai-title`, `cost-state`, `file-history-snapshot`, `api-request*`, `queue-operation`, `pr-link`, `agent-name`). Los transcripts de subagentes también lo traen | El scanner toma el `cwd` de la entrada de conversación donde aparece el `tool_use`, nunca de una entrada de metadata (resolución de paths relativos de `git worktree add`) |
| Prefijo de tools de un MCP | Confirmado: `mcp__<server>__<tool>`, con el nombre del servidor tal cual (`mcp__postman__`, `mcp__clickup__`, `mcp__codebase-memory-mcp__`, guiones incluidos). Los de plugin son `mcp__plugin_<plugin>_<server>__` y los conectores de claude.ai `mcp__claude_ai_<Nombre>__`. El transcript no distingue el scope del servidor | Servidor `neugram` → `mcp__neugram__<tool>`; el scanner compara por nombre exacto |
| Tamaño de los `.jsonl` | 1.087 archivos: p50 988 KB, p95 3,68 MB, p99 7,78 MB, máx 13,1 MB | Leer en reversa en `stop` es necesario, no una optimización; leer entero solo en `subagent-stop` |
| `SessionStart` trae `session_id` y `source` | Confirmado: `session_id`, `transcript_path`, `cwd`, `source`. Los `source` son cinco: `startup`, `resume`, `clear`, `compact` y **`fork`** (sesión nacida con `--fork-session`; desde una versión reciente deja de reportarse como `resume`). En la muestra aparecen `startup` 644, `clear` 197, `resume` 62, `compact` 52 | El matcher y el contrato de `session-start` incluyen `fork`: una sesión forkeada tiene id nuevo y necesita su línea `CLAUDE_CODE_SESSION_ID=` igual que `clear` |
| El id de sesión del proceso MCP sobrevive a `/clear` | No verificable sin lanzar sesiones interactivas; el razonamiento (el proceso MCP no se reinicia con `/clear`) y el riesgo de un fallback silencioso a la sesión anterior bastan para decidir | `session_id` explícito en las tools, sin lectura del entorno (`specs/api.md#convenciones`) |
