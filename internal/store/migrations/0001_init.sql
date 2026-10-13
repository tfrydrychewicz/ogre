-- The schema of the implementation plan, section 3.4.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT    NOT NULL UNIQUE,
  display_name  TEXT    NOT NULL,
  role          TEXT    NOT NULL CHECK (role IN ('user', 'admin')),
  status        TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deactivated')),
  created_at    TEXT    NOT NULL
);

CREATE TABLE links (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,    -- always lowercase: 'on-call'
  description  TEXT    NOT NULL DEFAULT '',
  owner_id     INTEGER NOT NULL REFERENCES users(id),
  version      INTEGER NOT NULL DEFAULT 1, -- optimistic concurrency
  created_at   TEXT    NOT NULL,
  updated_at   TEXT    NOT NULL
);

CREATE TABLE routes (
  id          INTEGER PRIMARY KEY,
  link_id     INTEGER NOT NULL REFERENCES links(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,            -- tie-break order of PRD 5.5
  pattern     TEXT    NOT NULL,            -- canonical text: 'gh/{repo}/pr/{num:int}'
  shape       TEXT    NOT NULL,            -- pattern with variable names removed
  destination TEXT    NOT NULL,
  UNIQUE (link_id, shape)
);
CREATE INDEX routes_by_link ON routes (link_id, position);

CREATE TABLE variables (                   -- only what the pattern text doesn't carry
  route_id    INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  example     TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (route_id, name)
);

CREATE TABLE revisions (                   -- written on every change from L2 on
  id        INTEGER PRIMARY KEY,
  link_id   INTEGER NOT NULL,              -- no cascade: history outlives the link
  editor_id INTEGER NOT NULL REFERENCES users(id),
  at        TEXT    NOT NULL,
  action    TEXT    NOT NULL,              -- create, update, delete, restore, import, transfer
  snapshot  TEXT    NOT NULL               -- the whole link as JSON, after the change
);
CREATE INDEX revisions_by_link ON revisions (link_id, id);
