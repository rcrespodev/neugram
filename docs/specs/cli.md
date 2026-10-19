# Spec — CLI

Binario `neugram`. Todos los subcomandos de operación hablan con el daemon en
`:7438` y lo levantan si hace falta; ninguno abre `neugram.db` directamente
salvo `serve`. Salida humana por defecto, `--json` en todos los listados.

```
neugram serve [--addr 127.0.0.1:7438]
neugram mcp
neugram hook <session-start|user-prompt-submit|stop|subagent-stop|session-end>

neugram task ls [--status S] [--product P] [--parent <key>] [--all]
neugram task add <key> "<título>" [--product P] [--parent <key>] [--tracker clickup:<id>:<url>] [--<custom> v]
neugram task show <key>
neugram task edit <key> [--title "<título>"] [--product P] [--notes N] [--<custom> v]
neugram task status <key> <todo|in-progress|review|blocked|done>
neugram task mv <key> --parent <key|none>   # recuelga la tarea con su subárbol; none la vuelve épico
neugram task tree <key>                     # el subárbol de <key>, hasta tres niveles, con status
neugram task wt <key>                       # worktrees con vivo/muerto
neugram task pin <key|none> [--session <uuid>]   # pinned_by=cli; para reparar

neugram product ls
neugram product add "<nombre>" [--description D]

neugram schema ls [--table T]
neugram schema add <table> <name> <type> [--required] [--enum a,b,c] [--indexed] --description "…"
neugram schema rm <table> <name> [--yes]    # DROP COLUMN; pide confirmación
neugram schema export > schema.yaml
neugram schema import schema.yaml [--dry-run]

neugram instructions show [--product P]     # generada + libre, tal como la ve el agente
neugram instructions edit [--product P]     # $EDITOR sobre la parte libre
neugram instructions export / import

neugram gate ls
neugram gate enable <name> [--params '{...}']
neugram gate disable <name>

neugram import-legacy [--dry-run] [--engram-db ~/.engram/engram.db]
neugram doctor
neugram version
```

## Comportamientos que no son obvios

- `NEUGRAM_HOME` (default `~/.neugram`) mueve `neugram.db`, `serve.log` y `hooks.log`; `NEUGRAM_ADDR` (default `127.0.0.1:7438`) mueve el puerto. El daemon que levanta el CLI hereda los dos, así que una base de prueba nunca toca la real.
- Los listados piden páginas de 200 al daemon hasta traer todo: `task ls` no tiene tope. Sin `--status` ni `--all` lista lo que no está en `done`, ordenado por `key` byte a byte.
- `task add` con una `key` existente falla; para cambiar título o producto está `task edit <key> [--title] [--product]` (mismo comando, flags opcionales).
- `task status` es la única forma, junto con la tool `task_status`, de cambiar el status. Ningún hook lo toca.
- `schema add` corre el DDL en el momento y regenera el JSON Schema del MCP; las sesiones MCP ya abiertas lo ven en la próxima llamada a `tools/list`.
- `schema rm` no se ejecuta si hay filas con valor no nulo en esa columna, salvo `--yes --force`.
- `instructions edit` guarda al cerrar el editor; si el archivo queda vacío, borra el override de producto o deja la global vacía con aviso.
- `doctor` corre contra la única base: tamaño de `neugram.db`, integridad de FTS (`observations_fts`/`prompts_fts` vs. sus tablas fuente), gates activos, última importación (`meta.last_import_at`) y tareas con `parent_task_id` roto (padre borrado sin `RESTRICT`, lo que no debería pasar salvo edición directa de la BD).
- `import-legacy` es idempotente: correrlo dos veces no duplica; ver `specs/import-legacy.md`.
- La jerarquía tiene tres niveles fijos: épico → tarea → sub-tarea. Ninguno de los tres comandos los distingue por nombre; el nivel es cuántos padres tiene la fila.
- `task add --parent <key>` falla si `<key>` no existe o ya está en el nivel 3 (`el padre ya está en el nivel 3`); sin `--product`, copia el `product_id` del padre en el momento de creación, sin volver a sincronizarlo después.
- `task mv --parent <key>` **arrastra el subárbol de la tarea movida**, y por eso falla en dos casos más que `task add`: colgarla de una tarea de nivel 2 cuando ella misma tiene hijas (`la tarea quedaría en el nivel 3 y ya tiene sub-tareas propias`) y colgarla de un épico cuando su subárbol llega a nietos (`la tarea quedaría en el nivel 2 y su subárbol llega a nietos`). El CLI no reacomoda nada: para mover un subárbol alto hay que desarmarlo primero, hoja por hoja, con `task mv` sobre las hijas.
- `task mv --parent none` desengancha la tarea y la vuelve épico. Nunca falla por profundidad: subir de nivel no puede desbordar el tope.
- `task tree <key>` dibuja el subárbol completo de `<key>`, sea de la altura que sea: sobre un épico son los tres niveles, sobre una sub-tarea es una sola línea.
- `task status <key> done` con hijas directas en un status distinto de `done` igual cambia el status y solo imprime un aviso: nadie más que el usuario decide cuándo una tarea está cerrada. El aviso cuenta hijas directas, no el subárbol.
