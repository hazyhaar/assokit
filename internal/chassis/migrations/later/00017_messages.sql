-- +goose Up
-- Messagerie privée membre↔membre (S4). Conversations implicites par paire
-- (sender_id, recipient_id) — pas de table conversations (KISS).
CREATE TABLE IF NOT EXISTS messages (
  id           TEXT PRIMARY KEY,
  sender_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body_md      TEXT NOT NULL,
  body_html    TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  read_at      TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_messages_pair  ON messages(sender_id, recipient_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_inbox ON messages(recipient_id, read_at);

-- +goose Down
DROP INDEX IF EXISTS idx_messages_inbox;
DROP INDEX IF EXISTS idx_messages_pair;
DROP TABLE IF EXISTS messages;
