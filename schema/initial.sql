-- DDL complet nonpossumus v1
-- FTS5 triggers en mode external content : utiliser INSERT INTO nodes_fts(nodes_fts, ...) VALUES('delete', ...)
-- et non DELETE FROM nodes_fts WHERE col=val (sinon résultats fantômes après update). Source: audit moe §R1 / D5.

CREATE TABLE nodes (
  id            TEXT PRIMARY KEY,
  parent_id     TEXT REFERENCES nodes(id) ON DELETE CASCADE,
  slug          TEXT NOT NULL UNIQUE,
  type          TEXT NOT NULL CHECK(type IN ('folder','page','post','form','doc')),
  title         TEXT NOT NULL,
  body_md       TEXT NOT NULL DEFAULT '',
  body_html     TEXT NOT NULL DEFAULT '',
  visibility    TEXT NOT NULL DEFAULT 'public'
                CHECK(visibility IN ('public','members','role')),
  author_id     TEXT REFERENCES users(id),
  display_order INTEGER NOT NULL DEFAULT 0,
  depth         INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at    TEXT
) STRICT;

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  display_name  TEXT NOT NULL,
  is_active     INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE roles (
  id    TEXT PRIMARY KEY,
  label TEXT NOT NULL
) STRICT;

CREATE TABLE user_roles (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
) STRICT;

CREATE TABLE node_permissions (
  node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  role_id    TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  permission TEXT NOT NULL CHECK(permission IN ('none','read','write','moderate','admin')),
  PRIMARY KEY (node_id, role_id)
) STRICT;

CREATE VIRTUAL TABLE nodes_fts USING fts5(
  node_id UNINDEXED, title, body_md,
  content='nodes', content_rowid='rowid'
);

CREATE TABLE node_reads (
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id      TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  last_read_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, node_id)
) STRICT;

CREATE TABLE signups (
  id           TEXT PRIMARY KEY,
  email        TEXT NOT NULL,
  display_name TEXT NOT NULL,
  profile      TEXT NOT NULL,
  fields_json  TEXT NOT NULL DEFAULT '{}',
  message      TEXT NOT NULL DEFAULT '',
  ip_hash      TEXT NOT NULL DEFAULT '',  -- SHA256(IP + COOKIE_SECRET), pas IP brute (RGPD)
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  processed_at TEXT
) STRICT;

CREATE TABLE email_outbox (
  id          TEXT PRIMARY KEY,
  to_addr     TEXT NOT NULL,
  subject     TEXT NOT NULL,
  body_text   TEXT NOT NULL DEFAULT '',
  body_html   TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending'
              CHECK(status IN ('pending','sent','failed')),
  attempts    INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT '',
  retry_after TEXT,
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  sent_at     TEXT
) STRICT;

-- Tokens magic link activation (signup → accès forum member). Source: brief M4 + audit coder-2 A2.
CREATE TABLE activation_tokens (
  token       TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TEXT NOT NULL,
  used_at     TEXT,
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE horui_config (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

-- Index de performance
CREATE INDEX idx_nodes_parent ON nodes(parent_id, display_order) WHERE deleted_at IS NULL;
CREATE INDEX idx_nodes_slug ON nodes(slug);
CREATE INDEX idx_user_roles_user ON user_roles(user_id);
CREATE INDEX idx_node_perms_node ON node_permissions(node_id);
CREATE INDEX idx_signups_email ON signups(email);
CREATE INDEX idx_outbox_status ON email_outbox(status, created_at) WHERE status = 'pending';
CREATE INDEX idx_activation_tokens_user ON activation_tokens(user_id);

-- Triggers FTS5 external content (mode content='nodes').
-- ATTENTION : ne pas utiliser DELETE FROM nodes_fts WHERE col=val — la table FTS5 external
-- content ignore ces suppressions. Utiliser INSERT ... VALUES('delete', ...) (voir D5).
CREATE TRIGGER nodes_fts_insert AFTER INSERT ON nodes BEGIN
  INSERT INTO nodes_fts(rowid, node_id, title, body_md)
    VALUES (new.rowid, new.id, new.title, new.body_md);
END;

CREATE TRIGGER nodes_fts_delete AFTER DELETE ON nodes BEGIN
  INSERT INTO nodes_fts(nodes_fts, rowid, node_id, title, body_md)
    VALUES ('delete', old.rowid, old.id, old.title, old.body_md);
END;

CREATE TRIGGER nodes_fts_update AFTER UPDATE ON nodes BEGIN
  INSERT INTO nodes_fts(nodes_fts, rowid, node_id, title, body_md)
    VALUES ('delete', old.rowid, old.id, old.title, old.body_md);
  INSERT INTO nodes_fts(rowid, node_id, title, body_md)
    VALUES (new.rowid, new.id, new.title, new.body_md);
END;
