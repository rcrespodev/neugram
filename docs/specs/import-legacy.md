# Spec — `import-legacy` y corte

Migra el estado del flujo actual (project `tasks` de Engram, parseado hoy por
`engram_tasks_store.py`) a las tablas de neugram, sin borrar nada en Engram.

Medido el 2026-09-17: 284 registros en `tasks` (110 session, 70 wt, 51
handoff, 25 identity, 21 status, 7 otros); ~6.500 observaciones en total.

## Fuente

`GET http://127.0.0.1:7437/observations?project=tasks&limit=2000`, que es el
único listado sin tope. Los filtros `type`/`topic_key` de ese endpoint se
ignoran, así que todo se filtra en cliente por `topic_key`. Un project
inexistente devuelve `null`, no `[]`.

Para el `task_id` de las observaciones comunes: lectura por ATTACH de
`engram.observations` completa, buscando el pie en `content`.

## Reglas de parseo

Regex sobre `topic_key`, como el store legacy (`split_key`): `identity` y
`status` sin sufijo; `session`, `wt` y `handoff` con sufijo obligatorio. Lo que
no encaja se cuenta en `otros` y se ignora. Cuando hay varios registros para la
misma key gana el más nuevo por `id`.

| `topic_key` | Destino | Mapeo |
|---|---|---|
| `task/<id>/identity` | `tasks` | `**Task**: <KEY> — <título>` → `key`, `title`; `**Product**: <texto>` → `products.name` (upsert por nombre) y `product_id`; `**Tracker**: clickup · id <x> · <url>` → `tracker_kind/id/url`. Lo demás del content → `notes`. La `key` se toma del pie (con mayúsculas), no del `topic_key` (minúsculas) |
| `task/<id>/status` | `tasks.status` | Primera palabra del content si está en el enum; si no, `todo` y aviso |
| `task/<id>/session/<uuid>` | `sessions` | `id=uuid`, `task_id`, `pinned_at=created_at`, `pinned_by=import`; `**Where**: <cwd>` → `cwd` si existe |
| `task/none/session/<uuid>` | `sessions` | `task_id NULL`, `pinned_at=created_at` |
| `task/<id>/wt/<basename>` | `worktrees` | Fence ```json con `path, repo, branch, base, created_by_session, purpose`. Registros viejos con `**Path**: …` / `Path:` / `- Path:` se parsean línea a línea. Upsert por `path` |
| `task/<id>/handoff/<uuid>` | `observation_ext` | `sync_id` del registro, `task_id`, `kind=handoff`, `session_id=uuid`. La observación no se mueve ni se copia |

Tarea referida por un pin, wt o handoff sin `identity`: se crea en `tasks` con
`title = key` y `status = todo`, y se lista al final como "sin identity".

## Observaciones comunes

Para cada fila de `engram.observations` con `deleted_at IS NULL` y `project !=
'tasks'` cuya última línea no vacía matchea `**Task**: <KEY> — …`, se inserta
`observation_ext(sync_id, task_id, session_id)`. `**Task**: ninguna — …` inserta
la fila con `task_id NULL` para distinguirla de una observación sin pie. Una
`KEY` que no existe en `tasks` se crea como arriba.

## Idempotencia

Cada destino se upsertea por su clave natural (`tasks.key`, `sessions.id`,
`worktrees.path`, `observation_ext.sync_id`, `products.name`). Correr dos
veces da el mismo resultado. `meta.last_import_at` guarda la última corrida.

## Salida

`--dry-run` imprime el mismo resumen sin escribir:

```
tasks        25 nuevas   0 actualizadas   3 sin identity (creadas desde pin/wt)
products      6
sessions    110 (97 con tarea, 13 none)
worktrees    70 (61 vivos, 9 muertos)
handoffs     51 vinculados
observations 4.812 con pie → task_id · 1.300 sin pie · 58 con key desconocida
otros         7 registros ignorados: <lista de topic_keys>
```

## Corte

Después de un `import-legacy` real y un `neugram doctor` limpio, un solo edit a
`~/.claude/settings.json` y `~/.claude.json`:

1. Quitar el MCP `plugin_engram_engram` (o deshabilitar el plugin) y agregar `neugram` con `command: neugram, args: [mcp]`.
2. Quitar los hooks `~/.claude/hooks/engram-tasks-session`, `~/.claude/hooks/engram-save-gate`, `~/.claude/hooks/engram-record-gate` y los del plugin de Engram; agregar los cinco de `specs/hooks.md`.
3. Reemplazar la sección `## Engram — persistent memory` del CLAUDE.md global por dos líneas que apunten a `neugram instructions show`.
4. Apagar el stack Engram Cloud local (`docker compose -f ~/.claude/plugins/marketplaces/engram/docker-compose.cloud.yml down`) y quitar la config de sync del Engram local.

Los skills `/handoff`, `/daily` y `review-wt` se adaptan en el mismo commit:
`/handoff` llama `handoff_save`; `/daily` y `review-wt` leen `task_show` en vez
de parsear `identity`.

Vuelta atrás: revertir el commit de config. Los registros legacy siguen en
Engram intactos; lo que se pierde es lo guardado con neugram entre el corte y
la vuelta, que queda en `neugram.db` para reimportar cuando se vuelva a cortar.
