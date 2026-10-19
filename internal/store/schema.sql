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

