# Spec — Hooks

`neugram hook <evento>` lee el JSON que Claude Code manda por stdin, llama al
daemon en `:7438` (lo levanta si no responde, con `neugram serve` desatachado
y espera hasta 2 s a `/health`), e imprime lo que Claude espera. Cada hook
termina en menos de 200 ms cuando el daemon ya corre.

Los cinco eventos reemplazan a `engram-tasks-session` (SessionStart),
`engram-save-gate` (Stop), `engram-record-gate` (PreToolUse sobre `mem_save`)
y a los hooks del plugin de Engram.

## Cableado en `~/.claude/settings.json`

```json
"SessionStart":      [{ "matcher": "startup|resume|clear|compact", "hooks": [{ "type": "command", "command": "neugram hook session-start", "timeout": 10 }] }],
"UserPromptSubmit":  [{ "hooks": [{ "type": "command", "command": "neugram hook user-prompt-submit", "timeout": 5 }] }],
"Stop":              [{ "hooks": [{ "type": "command", "command": "neugram hook stop", "timeout": 10 }] }],
"SubagentStop":      [{ "hooks": [{ "type": "command", "command": "neugram hook subagent-stop", "timeout": 10 }] }],
"SessionEnd":        [{ "hooks": [{ "type": "command", "command": "neugram hook session-end", "timeout": 5 }] }]
```

## `session-start`

Entrada: `session_id`, `cwd`, `source` (`startup|resume|clear|compact`).

1. Upsert local de `sessions(id, project, directory, started_at)` si la fila no existe; `project` sale de `internal/project` (derivado del git remote o del directorio). En `resume`/`compact` la fila ya existe y conserva su pin.
2. Salida a stdout, en este orden:
   - `CLAUDE_CODE_SESSION_ID=<id>`: la sesión no puede leer su propio id; el hook es la única fuente.
   - Instrucciones: parte generada + parte libre (`specs/instructions-and-gates.md`), con override del producto de la tarea pineada si la hay.
   - Estado del pin: `Pin: <key> — <título> [<status>]`, `Pin: none`, o `Sin pin todavía. Fijalo con task_pin antes de tocar archivos.` seguido de las tareas abiertas (`status != done`) con producto y status.
   - Último handoff de la tarea pineada, content entero. Si no hay pin, el handoff más reciente cuya sesión tenía el mismo `cwd`, marcado como pista.
   - Worktrees registrados de la tarea pineada con vivo/muerto.

## `user-prompt-submit`

Entrada: `session_id`, `prompt`.

`POST /prompts {session_id, content}` al daemon (`specs/api.md`). Se omite si
el prompt tiene menos de 10 caracteres sin espacios. No imprime nada. Un fallo
no bloquea el prompt: se loguea.

## `stop`

Entrada: `session_id`, `transcript_path`, `stop_hook_active`.

Si `stop_hook_active` es `true` termina con exit 0 (evita el bucle). Si no:

1. El daemon lee el transcript desde el último mensaje humano y extrae: herramientas `Edit`/`Write`/`NotebookEdit` con sus paths; `Bash` cuyo comando pinta mutante (`git worktree add`, `>`/`>>`, `mv`, `rm`, `sed -i`, `mkdir`, `touch`, `go mod`, `npm i`, …; lista fija en código, misma heurística del gate legacy); llamadas a `mem_save`/`mem_update`/`handoff_save` de neugram; llamadas a `worktree_register` y `progress_write`.
2. Aplica cada gate habilitado (`specs/instructions-and-gates.md`).
3. Si alguno falla, imprime JSON `{"decision":"block","reason":"<markdown>"}` y sale con 0. El `reason` nombra el gate, lo que vio, y la llamada exacta que lo destraba (por ejemplo `task_pin {"task_id":"TRIPULSE-3652"}` con las candidatas abiertas). Si todos pasan, sale con 0 sin salida.

Los subagentes heredan `session_id`, así que heredan el pin; los gates de pin
nunca les piden un pin propio.

## `subagent-stop`

Misma entrada y lógica que `stop`, con tres diferencias: no aplica
`require-save-on-mutation` (el subagente reporta al padre, que guarda); aplica
`require-progress-report`, que solo existe acá; y escribe en el log qué
subagente mutó qué, para el `stop` del padre.

El transcript que se escanea es el del subagente, no el del padre, y se lee
entero: un subagente no tiene mensajes humanos que marquen el inicio del turno.
Un bloqueo le devuelve el control al subagente una sola vez; con
`stop_hook_active` el segundo intento termina igual.

## `session-end`

Entrada: `session_id`, `reason`.

`POST /sessions/{id}/end` al daemon y `sessions.ended_at = now`. No imprime.

## Registro

Cada hook agrega una línea a `~/.neugram/hooks.log`: evento, sesión, duración,
gates evaluados y resultado. Es lo que se mira cuando "el hook bloqueó y no sé
por qué".
