# Spec — Instrucciones y gates

## Instrucciones

El bloque que `session-start` inyecta se arma en el daemon a partir de dos
fuentes y se muestra con `neugram instructions show [--product X]`.

**En el paso 3 solo existe la parte generada.** `GET /instructions` la sirve
sin `schema_fields` (todavía no existen) y la parte libre viene vacía; no hay
`instructions edit` ni texto transicional del CLAUDE.md, porque no hay corte
(`roadmap.md`). La parte libre, el override por producto y los campos custom
entran en el paso 4.

### Parte generada

Se deriva de `schema_fields` y del estado de `gates`. Se regenera en cada
llamada; nunca se edita a mano. Contiene:

- La lista de tools de flujo y para qué sirve cada una, en una línea; en el paso 3, las que existen. Incluye que toda tool que escribe pide `session_id`, tomado de la primera línea de `session-start`.
- Por cada tabla con campos custom: nombre, tipo, si es obligatorio, valores permitidos y descripción, tal como están en `schema_fields`.
- Los gates habilitados, cada uno con la frase que explica qué bloquea.
- Los tipos válidos de observación: `bugfix` · `decision` · `architecture` · `discovery` · `pattern` · `config` · `preference` · `handoff` · `session_summary`.
- El formato del pie `**Task**`, que neugram escribe solo pero el agente debe respetar si lo incluye.

### Parte libre

Markdown en `instructions.body_md`. Una fila global (`product_id NULL`) y
opcionalmente una por producto. Cuando la tarea pineada tiene producto con
override, el override **reemplaza** a la global; no se concatenan. Se edita con
`neugram instructions edit [--product X]`, que abre `$EDITOR`, y se versiona
con `instructions export/import`.

Migra acá la sección `## Engram — persistent memory` del CLAUDE.md global,
menos lo que la parte generada ya cubre (formato del pie, topic_key, tipos).
De la sección Delegation migra solo el contrato del artefacto `progress/`:
queda una línea que dice que el padre asigna un `delegation_id` y que el
subagente que muta cierra con `progress_write`. La política de delegación se
queda en el CLAUDE.md (ADR 0010).

## Gates

Set fijo. Cada gate es una función Go `func(ctx TurnContext) *Violation` y una
fila en `gates` con `enabled` y `params`. Se prenden y apagan con
`neugram gate enable|disable <nombre>`. Sin DSL ni gates definidos por el
usuario (ADR 0009).

| Nombre | Default | Falla cuando | `params` |
|---|---|---|---|
| `require-task-pin-on-mutation` | on | El turno mutó archivos y la sesión no tiene pin (`pinned_at IS NULL`). Pin a `none` cuenta como pin | — |
| `require-save-on-mutation` | on | El turno mutó archivos y no hubo ningún `mem_save`, `mem_update` ni `handoff_save` en el turno. El `reason` lista los archivos tocados | `{"min_files": 1}` |
| `require-worktree-registered` | on | El turno ejecutó `git worktree add` (o `EnterWorktree`) y no hubo `worktree_register` con ese path en el turno | — |
| `require-field` | off | Un `mem_save` del turno omitió el campo custom nombrado. Redundante con `required=1` en `schema_fields`, que ya rechaza el save; este gate existe para campos que se quieren exigir solo en ciertos productos | `{"field": "impact", "products": ["Portal"]}` |
| `require-progress-report` | on | Solo en `subagent-stop`. El subagente mutó archivos y no hubo un `progress_write` exitoso en su transcript. El `reason` lista los archivos tocados y la llamada con los campos obligatorios | — |
| `block-topic-key-collision` | off en el paso 3; se revisa en el paso 4 | Un `mem_save` trae `topic_key` que ya existe en el mismo project con distinto `sync_id`. Se evalúa en el daemon antes del `POST`, no en el `Stop`; el gate solo decide si se rechaza o se deja pasar como upsert | — |

**Worktree con path no expandible.** `require-worktree-registered` compara el
path de cada worktree creado contra los `worktree_register` exitosos del turno.
Un `git worktree add` con `Unresolved: true` no se puede comparar: el gate se
satisface si hubo al menos un `worktree_register` en el turno; si no, bloquea
citando el comando tal como se escribió y pidiendo el path absoluto.
`require-save-on-mutation` no cuenta `worktree_register` como guardado.

`block-topic-key-collision` arranca apagado porque choca con el protocolo
vigente: los registros que se actualizan por `topic_key` (y el reintento de un
reporte de progreso, que reusa la key) serían todos 409 vía `mem_save`. Se
revisa en el paso 4, con `progress_write` y `mem_update` como camino de
actualización.

En el paso 3, `subagent-stop` aplica solo `require-task-pin-on-mutation` y
`require-worktree-registered`; `require-progress-report` entra en el paso 4
(`specs/hooks.md#subagent-stop`).

El set de gates y sus defaults viven en código. Una fila de `gates` solo
sobreescribe `enabled` y `params`, y sin filas rige el default; `gate
ls/enable/disable` es del paso 4. Ningún cambio del paso 3 modifica el DDL.

Un gate que haga falta dos veces se agrega al set fijo con un ADR corto.

### Forma del bloqueo

```json
{
  "decision": "block",
  "reason": "**require-task-pin-on-mutation**\n\nEl turno editó `docs/specs/hooks.md` y la sesión no tiene pin.\n\nTareas abiertas:\n- TRIPULSE-3652 — Best Skills [in-progress]\n- engram-fork-debat — Fork personal de Engram [in-progress]\n\nDestrabar: `task_pin {\"task_id\": \"<key>\"}` o `task_pin {\"task_id\": \"none\"}`."
}
```

Un solo bloqueo por turno: el primero que falla, en el orden de la tabla.
