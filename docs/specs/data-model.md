# Spec — Modelo de datos

BD propia: `~/.neugram/neugram.db`, SQLite (modernc.org/sqlite), WAL,
`foreign_keys=ON`. Engram attacheada como schema `engram` en solo lectura
(`specs/engram-boundary.md`). Ids propios: ULID en texto. Todas las tablas
llevan `created_at` y `updated_at` en ISO 8601 UTC.

## Tablas fijas

```sql
CREATE TABLE products (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  description TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE tasks (
  id          TEXT PRIMARY KEY,
  key         TEXT NOT NULL UNIQUE COLLATE NOCASE,  -- TRIPULSE-3652, engram-fork-debat
  title       TEXT NOT NULL,
  product_id  TEXT REFERENCES products(id),
  status      TEXT NOT NULL DEFAULT 'todo'
              CHECK (status IN ('todo','in-progress','review','blocked','done')),
  tracker_kind TEXT,                                -- clickup, github, none
  tracker_id   TEXT,
  tracker_url  TEXT,
  notes       TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX tasks_status ON tasks(status);
CREATE INDEX tasks_product ON tasks(product_id);

CREATE TABLE sessions (
  id          TEXT PRIMARY KEY,                     -- CLAUDE_CODE_SESSION_ID; mismo valor que engram.sessions.id
  task_id     TEXT REFERENCES tasks(id),            -- NULL + pinned_at NOT NULL = pin a "none"
  pinned_at   TEXT,                                 -- NULL = sin pin
  pinned_by   TEXT CHECK (pinned_by IN ('agent','hook','cli','import')),
  cwd         TEXT,
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX sessions_task ON sessions(task_id);

CREATE TABLE worktrees (
  id                 TEXT PRIMARY KEY,
  task_id            TEXT REFERENCES tasks(id),     -- NULL = worktree de trabajo sin ticket
  path               TEXT NOT NULL UNIQUE,
  repo               TEXT NOT NULL,
  branch             TEXT NOT NULL,
  base               TEXT,
  purpose            TEXT,
  created_by_session TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);
CREATE INDEX worktrees_task ON worktrees(task_id);

CREATE TABLE observation_ext (
  sync_id     TEXT PRIMARY KEY,                     -- engram.observations.sync_id, nunca id
  task_id     TEXT REFERENCES tasks(id),            -- NULL = "ninguna"
  kind        TEXT CHECK (kind IN ('handoff')),     -- NULL para una observación común
  session_id  TEXT,                                 -- copia de engram para no depender del JOIN en listados
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX observation_ext_task ON observation_ext(task_id);
CREATE INDEX observation_ext_kind ON observation_ext(kind) WHERE kind IS NOT NULL;

CREATE TABLE progress_reports (
  id             TEXT PRIMARY KEY,
  session_id     TEXT NOT NULL REFERENCES sessions(id), -- heredado por el subagente
  delegation_id  TEXT NOT NULL,                     -- lo asigna el padre; no es tasks.key
  task_id        TEXT REFERENCES tasks(id),         -- pin de la sesión al escribir; NULL = "ninguna"
  worktree_id    TEXT REFERENCES worktrees(id),     -- el registrado que contiene el cwd, si hay
  role           TEXT NOT NULL CHECK (role IN ('explore','impl','review')),
  status         TEXT NOT NULL CHECK (status IN ('done','blocked')),
  title          TEXT NOT NULL,
  summary        TEXT NOT NULL,                     -- JSON array de strings, 5 a 10
  next_steps     TEXT NOT NULL,                     -- JSON array de strings, 3 a 6
  refs           TEXT,                              -- JSON {engram_topics, files, links}; files como path:line
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  UNIQUE (session_id, delegation_id)
);
CREATE INDEX progress_reports_task ON progress_reports(task_id);

CREATE TABLE schema_fields (
  id           TEXT PRIMARY KEY,
  table_name   TEXT NOT NULL CHECK (table_name IN ('tasks','observation_ext','worktrees','products')),
  name         TEXT NOT NULL,                       -- snake_case, no colisiona con columnas fijas
  type         TEXT NOT NULL CHECK (type IN ('text','integer','real','bool','date','enum')),
  required     INTEGER NOT NULL DEFAULT 0,
  enum_values  TEXT,                                -- JSON array cuando type = enum
  description  TEXT NOT NULL,                       -- va al JSON Schema y a las instrucciones
  indexed      INTEGER NOT NULL DEFAULT 0,
  position     INTEGER NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (table_name, name)
);

CREATE TABLE instructions (
  id          TEXT PRIMARY KEY,
  product_id  TEXT REFERENCES products(id),         -- NULL = global
  body_md     TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE (product_id)
);

CREATE TABLE gates (
  name        TEXT PRIMARY KEY,
  enabled     INTEGER NOT NULL DEFAULT 1,
  params      TEXT,                                 -- JSON; ej. {"field":"impact"} para require-field
  updated_at  TEXT NOT NULL
);

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL                               -- schema_version, last_import_at, engram_db_path
);
```

## Columnas custom

Cada fila de `schema_fields` produce, al arrancar el daemon y al ejecutar
`schema add`, un `ALTER TABLE <table_name> ADD COLUMN <name> <sqlite_type>`
si la columna no existe, más `CREATE INDEX IF NOT EXISTS` cuando `indexed=1`.

| `type` | Tipo SQLite | Validación en `mem_save` / CLI |
|---|---|---|
| `text` | TEXT | string |
| `integer` | INTEGER | entero |
| `real` | REAL | número |
| `bool` | INTEGER | `true`/`false`, se guarda 0/1 |
| `date` | TEXT | ISO 8601 (`YYYY-MM-DD` o completo) |
| `enum` | TEXT | debe estar en `enum_values` |

`schema rm` hace `ALTER TABLE ... DROP COLUMN` (SQLite ≥ 3.35). Las columnas
custom nunca entran a un índice FTS. Nombres reservados: cualquier columna
fija de la tabla y `id`, `sync_id`, `created_at`, `updated_at`.

## Vistas de lectura

Solo el daemon las usa; se recrean al arrancar porque dependen del ATTACH.

```sql
CREATE TEMP VIEW v_observations AS
SELECT o.id, o.sync_id, o.session_id, o.type, o.title, o.content,
       o.project, o.scope, o.topic_key, o.created_at, o.updated_at,
       x.task_id, x.kind, t.key AS task_key, t.title AS task_title, t.product_id
FROM engram.observations o
LEFT JOIN observation_ext x ON x.sync_id = o.sync_id
LEFT JOIN tasks t ON t.id = x.task_id
WHERE o.deleted_at IS NULL;
```

Con columnas custom en `observation_ext`, la vista se genera con `x.*`
expandido desde `schema_fields` en vez de listar a mano.

## Invariantes

- `observation_ext.sync_id` siempre existe en `engram.observations`. Una fila huérfana (observación borrada en Engram) se lista en `neugram doctor` y no rompe nada.
- Una observación tiene cero o una tarea.
- `sessions.id` es el mismo string que `engram.sessions.id`; neugram crea las dos filas en SessionStart.
- `worktrees.path` es absoluto. Vivo = existe en disco; el registro no guarda liveness.
- `tasks.key` se compara sin distinguir mayúsculas; se guarda como el usuario la escribió la primera vez.
- Un reporte por `(session_id, delegation_id)`: repetir `progress_write` lo reemplaza. `task_id` se fija al escribir y no sigue un repin posterior.
