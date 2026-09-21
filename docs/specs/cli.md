# Spec — CLI

Binario `neugram`. Todos los subcomandos de operación hablan con el daemon en
`:7438` y lo levantan si hace falta; ninguno abre `neugram.db` directamente
salvo `serve`. Salida humana por defecto, `--json` en todos los listados.

```
neugram serve [--addr 127.0.0.1:7438] [--engram http://127.0.0.1:7437] [--engram-db ~/.engram/engram.db]
neugram mcp
neugram hook <session-start|user-prompt-submit|stop|subagent-stop|session-end>

neugram task ls [--status S] [--product P] [--all]
neugram task add <key> "<título>" [--product P] [--tracker clickup:<id>:<url>] [--<custom> v]
neugram task show <key>
neugram task status <key> <todo|in-progress|review|blocked|done>
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

neugram import-legacy [--dry-run] [--engram http://127.0.0.1:7437]
neugram doctor
neugram version
```

## Comportamientos que no son obvios

- `task add` con una `key` existente falla; para cambiar título o producto está `task edit <key> [--title] [--product]` (mismo comando, flags opcionales).
- `task status` es la única forma, junto con la tool `task_status`, de cambiar el status. Ningún hook lo toca.
- `schema add` corre el DDL en el momento y regenera el JSON Schema del MCP; las sesiones MCP ya abiertas lo ven en la próxima llamada a `tools/list`.
- `schema rm` no se ejecuta si hay filas con valor no nulo en esa columna, salvo `--yes --force`.
- `instructions edit` guarda al cerrar el editor; si el archivo queda vacío, borra el override de producto o deja la global vacía con aviso.
- `doctor` imprime lo de `specs/engram-boundary.md`: Engram vivo, columnas asumidas, huérfanos, gates activos, última importación, versión de Engram (`engram version`), tamaño de ambas BD.
- `import-legacy` es idempotente: correrlo dos veces no duplica; ver `specs/import-legacy.md`.
