-- +goose Up
-- Newsletters / diffusions (S?). Une diffusion = un email markdown composé par un
-- admin et envoyé à tous les membres actifs via l'outbox (deps.Mailer). Pas de
-- table recipients (KISS) : la traçabilité tient dans sent_at + recipients_count.
CREATE TABLE IF NOT EXISTS newsletters (
  id               TEXT PRIMARY KEY,
  subject          TEXT NOT NULL,
  body_md          TEXT NOT NULL,
  body_html        TEXT NOT NULL DEFAULT '',
  created_by       TEXT,
  sent_at          TEXT,
  recipients_count INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_newsletters_sent_at ON newsletters(sent_at);

-- +goose Down
DROP INDEX IF EXISTS idx_newsletters_sent_at;
DROP TABLE IF EXISTS newsletters;
