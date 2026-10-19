# Spec — Modelo de datos

Una sola base: `~/.neugram/neugram.db`, SQLite (modernc.org/sqlite), WAL,
`foreign_keys=ON`. No hay ATTACH ni segunda base (ADR 0011). Ids propios: ULID
en texto. Los ids heredados de Engram se conservan como estaban
(`specs/engram-provenance.md`). Todas las tablas llevan `created_at` y
`updated_at` en ISO 8601 UTC, salvo donde se aclara.

## Memoria

Heredadas de Engram, ahora propias. El DDL de acá manda; los deltas contra el
schema original están en `specs/engram-provenance.md`.

```sql
CREATE TABLE sessions (
  id          TEXT PRIMARY KEY,                     -- CLAUDE_CODE_SESSION_ID
  project     TEXT NOT NULL,                        -- repo, según internal/project
  directory   TEXT NOT NULL,                        -- cwd de la sesión
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  summary     TEXT,
  task_id     TEXT REFERENCES tasks(id),            -- NULL + pinned_at NOT NULL = pin a "none"
  pinned_at   TEXT,                                 -- NULL = sin pin
  pinned_by   TEXT CHECK (pinned_by IN ('agent','hook','cli','import')),
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX sessions_task ON sessions(task_id);
CREATE INDEX sessions_project ON sessions(project);

CREATE TABLE observations (
  id              INTEGER PRIMARY KEY AUTOINCREMENT, -- content_rowid de observations_fts
  sync_id         TEXT NOT NULL UNIQUE,              -- 'obs-' || hex(randomblob(16)); clave pública
  session_id      TEXT NOT NULL REFERENCES sessions(id),
  type            TEXT NOT NULL,
  title           TEXT NOT NULL,
  content         TEXT NOT NULL,
  project         TEXT NOT NULL,                     -- repo de procedencia
  scope           TEXT NOT NULL DEFAULT 'project'
                  CHECK (scope IN ('project','personal')),
  topic_key       TEXT,
  tool_name       TEXT,                              -- solo en capturas pasivas
  task_id         TEXT REFERENCES tasks(id),         -- NULL = "ninguna"
  kind            TEXT CHECK (kind IN ('handoff')),  -- NULL para una observación común
  normalized_hash TEXT,                              -- dedupe
  revision_count  INTEGER NOT NULL DEFAULT 1,
  duplicate_count INTEGER NOT NULL DEFAULT 1,
  last_seen_at    TEXT,
  review_after    TEXT,                              -- mem_review
  pinned          INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  deleted_at      TEXT                               -- soft delete
);
CREATE INDEX observations_task    ON observations(task_id);
CREATE INDEX observations_kind    ON observations(kind) WHERE kind IS NOT NULL;
CREATE INDEX observations_project ON observations(project);
CREATE INDEX observations_session ON observations(session_id);
CREATE INDEX observations_type    ON observations(type);
CREATE INDEX observations_created ON observations(created_at DESC);
CREATE INDEX observations_deleted ON observations(deleted_at);
CREATE INDEX observations_topic   ON observations(topic_key, project, scope, updated_at DESC);
CREATE INDEX observations_dedupe  ON observations(normalized_hash, project, scope, type, title, created_at DESC);

CREATE VIRTUAL TABLE observations_fts USING fts5(
  title, content, tool_name, type, project, topic_key,
  content='observations', content_rowid='id'
);

CREATE TABLE user_prompts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,      -- content_rowid de prompts_fts
  sync_id    TEXT NOT NULL UNIQUE,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  content    TEXT NOT NULL,
  project    TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX user_prompts_session ON user_prompts(session_id);
CREATE INDEX user_prompts_created ON user_prompts(created_at DESC);

CREATE VIRTUAL TABLE prompts_fts USING fts5(
  content, project,
  content='user_prompts', content_rowid='id'
);

CREATE TABLE memory_relations (
  id                        INTEGER PRIMARY KEY AUTOINCREMENT,
  sync_id                   TEXT NOT NULL UNIQUE,
  source_id                 TEXT REFERENCES observations(sync_id),
  target_id                 TEXT REFERENCES observations(sync_id),
  relation                  TEXT NOT NULL DEFAULT 'pending',
  reason                    TEXT,
  evidence                  TEXT,
  confidence                REAL,
  judgment_status           TEXT NOT NULL DEFAULT 'pending',
  marked_by_actor           TEXT,
  marked_by_kind            TEXT,
  marked_by_model           TEXT,
  session_id                TEXT REFERENCES sessions(id),
  superseded_at             TEXT,
  superseded_by_relation_id INTEGER REFERENCES memory_relations(id) ON DELETE SET NULL,
  created_at                TEXT NOT NULL,
  updated_at                TEXT NOT NULL
);
CREATE INDEX memory_relations_source ON memory_relations(source_id, judgment_status);
CREATE INDEX memory_relations_target ON memory_relations(target_id, judgment_status);
CREATE INDEX memory_relations_status ON memory_relations(judgment_status, created_at DESC);
```

Las dos tablas FTS son `external content`: no guardan los documentos, los leen
de la tabla fuente por `rowid`. Cada una lleva tres triggers que la mantienen
sincronizada, escritos desde el recetario de
`https://www.sqlite.org/fts5.html#external_content_tables`:

```sql
CREATE TRIGGER obs_fts_insert AFTER INSERT ON observations BEGIN
  INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, topic_key)
  VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.topic_key);
END;

CREATE TRIGGER obs_fts_delete AFTER DELETE ON observations BEGIN
  INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, topic_key)
  VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.topic_key);
END;

CREATE TRIGGER obs_fts_update AFTER UPDATE ON observations BEGIN
  INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project, topic_key)
  VALUES ('delete', old.id, old.title, old.content, old.tool_name, old.type, old.project, old.topic_key);
  INSERT INTO observations_fts(rowid, title, content, tool_name, type, project, topic_key)
  VALUES (new.id, new.title, new.content, new.tool_name, new.type, new.project, new.topic_key);
END;

CREATE TRIGGER prompt_fts_insert AFTER INSERT ON user_prompts BEGIN
  INSERT INTO prompts_fts(rowid, content, project)
  VALUES (new.id, new.content, new.project);
END;

CREATE TRIGGER prompt_fts_delete AFTER DELETE ON user_prompts BEGIN
  INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
  VALUES ('delete', old.id, old.content, old.project);
END;

CREATE TRIGGER prompt_fts_update AFTER UPDATE ON user_prompts BEGIN
  INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
  VALUES ('delete', old.id, old.content, old.project);
  INSERT INTO prompts_fts(rowid, content, project)
  VALUES (new.id, new.content, new.project);
END;
```

Van verbatim. La fila con el comando `'delete'` —la única del trigger de
DELETE, la primera del de UPDATE— es lo que no se puede omitir: sin ella el
índice acumula documentos fantasma y las búsquedas devuelven filas que ya no
existen, en silencio y sin que nada falle. Y la enumeración explícita de
columnas es lo que hace que agregar columnas a `observations` —`task_id`,
`kind` o cualquier campo custom— no toque el índice.

`normalized_hash` es el sha256 en hex del `content` pasado a minúsculas y con
cada corrida de espacios en blanco colapsada a un espacio. Es la clave de
dedupe junto con `project`, `scope`, `type` y `title`. La definición la fija el
dato importado, no el código nuevo: recomputada sobre 3.000 filas de la base
legacy reproduce el valor almacenado en las 3.000, y la verificación 6 de
`specs/import-legacy.md` la revalida antes de escribir nada.

`sync_id` conserva el nombre que le puso el cloud que ya no existe. No se
renombra: es la clave de unión de los 6.805 registros importados, y la
referencia de `memory_relations` y de los handoffs. `id` se conserva porque
FTS5 lo exige como `content_rowid`; nunca sale hacia el agente ni hacia la UI.

Un prompt no lleva `task_id`: se atribuye por el pin de su sesión, que ahora es
un JOIN en la misma base.

## Flujo

Propias desde el principio.

```sql
CREATE TABLE products (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  description TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE tasks (
  id             TEXT PRIMARY KEY,
  key            TEXT NOT NULL UNIQUE COLLATE NOCASE,  -- TRIPULSE-3652, engram-fork-debat
  title          TEXT NOT NULL,
  product_id     TEXT REFERENCES products(id),
  parent_task_id TEXT REFERENCES tasks(id) ON DELETE RESTRICT,
                 -- NULL = épico (nivel 1). Ver "Jerarquía" abajo.
  status         TEXT NOT NULL DEFAULT 'todo'
                 CHECK (status IN ('todo','in-progress','review','blocked','done')),
  tracker_kind TEXT,                                -- clickup, github, none
  tracker_id   TEXT,
  tracker_url  TEXT,
  notes       TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  CHECK (parent_task_id IS NULL OR parent_task_id != id)
);
CREATE INDEX tasks_status ON tasks(status);
CREATE INDEX tasks_product ON tasks(product_id);
CREATE INDEX tasks_parent ON tasks(parent_task_id);

-- Profundidad 3 y ausencia de ciclos: SQLite no admite subqueries en CHECK,
-- así que el CHECK de arriba solo cubre la auto-referencia; el resto va acá.
-- En el INSERT la fila todavía no existe, así que no puede tener descendientes:
-- alcanza con acotar el nivel del padre.
CREATE TRIGGER tasks_parent_depth_ins
BEFORE INSERT ON tasks
WHEN NEW.parent_task_id IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'parent_task_id: el padre ya está en el nivel 3 (máximo 3 niveles: épico → tarea → sub-tarea)')
    WHERE (SELECT parent_task_id FROM tasks
            WHERE id = (SELECT parent_task_id FROM tasks WHERE id = NEW.parent_task_id)) IS NOT NULL;
END;

CREATE TRIGGER tasks_parent_depth_upd
BEFORE UPDATE OF parent_task_id ON tasks
WHEN NEW.parent_task_id IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'parent_task_id: una tarea no puede ser su propio padre')
    WHERE NEW.parent_task_id = NEW.id;

  SELECT RAISE(ABORT, 'parent_task_id: el padre ya está en el nivel 3 (máximo 3 niveles: épico → tarea → sub-tarea)')
    WHERE (SELECT parent_task_id FROM tasks
            WHERE id = (SELECT parent_task_id FROM tasks WHERE id = NEW.parent_task_id)) IS NOT NULL;

  SELECT RAISE(ABORT, 'parent_task_id: la tarea quedaría en el nivel 3 y ya tiene sub-tareas propias')
    WHERE (SELECT parent_task_id FROM tasks WHERE id = NEW.parent_task_id) IS NOT NULL
      AND EXISTS (SELECT 1 FROM tasks WHERE parent_task_id = NEW.id);

  SELECT RAISE(ABORT, 'parent_task_id: la tarea quedaría en el nivel 2 y su subárbol llega a nietos')
    WHERE (SELECT parent_task_id FROM tasks WHERE id = NEW.parent_task_id) IS NULL
      AND EXISTS (SELECT 1 FROM tasks h JOIN tasks n ON n.parent_task_id = h.id
                   WHERE h.parent_task_id = NEW.id);
END;
```

### Jerarquía

- **Profundidad: 3 niveles fijos** — épico (nivel 1, `parent_task_id IS NULL`),
  tarea (nivel 2), sub-tarea (nivel 3). No es una jerarquía general: es un tope
  duro, igual que antes lo era el 2. Son niveles, no tipos: una fila de `tasks`
  con las mismas columnas y las mismas reglas de pin, status y tracker en los
  tres. Los dos triggers de arriba lo hacen cumplir, no solo lo documentan.
- **Al fijar el padre `P` de un nodo `X`** hay dos cosas que verificar, y
  ninguna necesita recursión: que `P` esté en el nivel 1 o 2 (dos lookups:
  el padre de `P` y el abuelo de `P`; si el abuelo existe, `P` ya es nivel 3),
  y que el subárbol de `X` quepa por debajo del nivel que le toca. Con `P` en
  el nivel 2, `X` va al 3 y no puede tener hijas propias; con `P` en el nivel
  1, `X` va al 2 y no puede tener nietos. La altura de `X` está acotada por el
  mismo tope, así que mirar hijas y nietos la agota.
- **Ciclos**: un ciclo no tiene raíz — ningún nodo del ciclo llega a un
  `parent_task_id IS NULL` subiendo. El chequeo de nivel sube exactamente dos
  pasos desde el padre buscando ese `NULL`, y aborta si a dos pasos todavía
  hay padre. Eso cubre todos los casos porque el arco que cerraría un ciclo es
  siempre `X.parent = P` con `P` descendiente de `X` o `P = X`, y con el tope
  en 3 no hay más que tres formas:
  - `P = X`: primer `RAISE` del trigger de UPDATE, y el `CHECK` en el INSERT.
  - `P` hija de `X`: si `X` está en el nivel 1, `P` está en el 2 y `X` tiene
    hijas — tercer `RAISE`. Si `X` está en el nivel 2 o 3, `P` está en el 3 y
    aborta el chequeo de nivel.
  - `P` nieta de `X`: `X` está en el nivel 1 y `P` en el 3 — chequeo de nivel.

  Más profundo es imposible: el tope ya lo impide. El `CHECK` de la tabla solo
  cubre la auto-referencia directa; el resto es trigger porque SQLite no
  evalúa subqueries dentro de un `CHECK`.
- **Producto**: no se hereda por constraint. `task add --parent` toma el
  `product_id` del padre como default en la capa CLI; la hija puede pasar
  `--product` explícito y queda desalineada del padre sin que nada lo impida.
  Mantiene la tabla simple; la UI muestra el producto de cada una.
- **Status**: sigue la regla general — lo declara el usuario, ningún hook lo
  deriva ni lo bloquea. Cerrar un padre con hijas abiertas no está prohibido;
  `task status <key> done` imprime un aviso si hay hijas `!= done`, nada más.
  El aviso cuenta las hijas directas, no el subárbol.
- **Pin y búsqueda**: una sesión pinea a cualquier `tasks.id`, de cualquier
  nivel, indistinto. `mem_search`/`mem_context` suben el árbol por default
  (`include_ancestors`, hasta dos ids extra) y lo bajan solo si se los pide
  (`include_subtasks`); `task show <key>` baja al subárbol entero. Todo con
  `IN` anidado, sin CTE recursivo: ver "Lecturas".
- **Borrado del padre**: `ON DELETE RESTRICT`. Borrar una tarea con hijas
  falla; hay que reasignarlas (`task mv --parent none|<key>`) o borrarlas
  primero, de abajo hacia arriba. No hay borrado en cascada.

```sql
CREATE TABLE worktrees (
  id                 TEXT PRIMARY KEY,
  task_id            TEXT REFERENCES tasks(id),     -- NULL = worktree de trabajo sin ticket
  path               TEXT NOT NULL UNIQUE,
  repo               TEXT NOT NULL,
  branch             TEXT NOT NULL,
  base               TEXT,
  purpose            TEXT,
  created_by_session TEXT,                         -- sin FK: hay registros legacy que apuntan a una sesión inexistente
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);
CREATE INDEX worktrees_task ON worktrees(task_id);

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
  table_name   TEXT NOT NULL CHECK (table_name IN ('tasks','observations','worktrees','products')),
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
  value TEXT NOT NULL                               -- schema_version, last_import_at, legacy_db_path
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
custom nunca entran a un índice FTS. Nombres reservados: cualquier columna fija
de la tabla destino, más `id`, `sync_id`, `created_at`, `updated_at`,
`deleted_at`.

## Lecturas

Sin ATTACH no hay vistas que recrear al arrancar. Traer una tarea entera
cruzando repos es un JOIN:

```sql
SELECT o.sync_id, o.type, o.title, o.content, o.project, o.created_at
FROM observations o
JOIN tasks t ON t.id = o.task_id
WHERE t.key = ? COLLATE NOCASE AND o.deleted_at IS NULL
ORDER BY o.created_at DESC;
```

Bajar el árbol —`task show`, y `mem_search`/`mem_context` con
`include_subtasks`— es el subárbol entero de la tarea pedida. Con el tope en
3, un `IN` anidado agota los tres niveles y no hace falta `WITH RECURSIVE`:

```sql
SELECT o.sync_id, o.type, o.title, o.content, o.task_id, o.created_at
FROM observations o
WHERE o.task_id IN (
  SELECT id FROM tasks
  WHERE id = :root_id
     OR parent_task_id = :root_id
     OR parent_task_id IN (SELECT id FROM tasks WHERE parent_task_id = :root_id)
) AND o.deleted_at IS NULL
ORDER BY o.created_at DESC;
```

Subir es simétrico y más barato: desde el nivel 3 son dos ids extra, el padre
y el abuelo. Es lo que hacen `mem_search`/`mem_context` con `include_ancestors`
(default) a partir de la `key` pedida:

```sql
SELECT o.sync_id, o.type, o.title, o.content, o.task_id, o.created_at
FROM observations o
WHERE o.task_id IN (
  SELECT t.id FROM tasks t
   WHERE t.key = :key COLLATE NOCASE
  UNION
  SELECT t.parent_task_id FROM tasks t
   WHERE t.key = :key COLLATE NOCASE AND t.parent_task_id IS NOT NULL
  UNION
  SELECT p.parent_task_id FROM tasks t JOIN tasks p ON p.id = t.parent_task_id
   WHERE t.key = :key COLLATE NOCASE AND p.parent_task_id IS NOT NULL
) AND o.deleted_at IS NULL
ORDER BY o.created_at DESC;
```

La cadena de ancestros como columnas, para la ficha y el breadcrumb de la UI,
son dos `LEFT JOIN`:

```sql
SELECT t.id AS self_id, p.id AS parent_id, g.id AS grandparent_id
FROM tasks t
LEFT JOIN tasks p ON p.id = t.parent_task_id
LEFT JOIN tasks g ON g.id = p.parent_task_id
WHERE t.key = :key COLLATE NOCASE;
```

`deleted_at IS NULL` filtra soft-deletes en toda lectura. La búsqueda de texto
entra por `observations_fts MATCH ?` y se cruza con las mismas columnas; los
filtros por tarea y por campo custom son `WHERE` del mismo query, sin topes.

## Invariantes

- Una observación tiene cero o una tarea.
- `sessions.id` es el `CLAUDE_CODE_SESSION_ID`, y es la única fila de esa sesión: un pin y una sesión de memoria son la misma cosa.
- Todo `sync_id` es único y no nulo en `observations`, `user_prompts` y `memory_relations`. Es el id que ven el agente, la UI y los reportes.
- `observations.id` y `user_prompts.id` no se exponen: son el `rowid` que FTS5 necesita.
- `worktrees.path` es absoluto. Vivo = existe en disco; el registro no guarda liveness.
- `tasks.key` se compara sin distinguir mayúsculas; se guarda como el usuario la escribió la primera vez.
- Un reporte por `(session_id, delegation_id)`: repetir `progress_write` lo reemplaza. `task_id` se fija al escribir y no sigue un repin posterior.
- `tasks.parent_task_id` tiene como máximo 3 niveles: ninguna fila tiene bisabuelo. Lo hacen cumplir `tasks_parent_depth_ins`/`_upd`, no el `CHECK`; mover un nodo arrastra su subárbol y falla si lo desborda.
- No hay ciclos: ningún nodo de un ciclo alcanza un `parent_task_id IS NULL` subiendo, y el chequeo de nivel del trigger aborta justamente cuando no lo encuentra en dos pasos.
- `tasks.product_id` no se sincroniza entre padre e hijo: es el valor con el que se creó o editó cada fila, sin trigger que los alinee.
