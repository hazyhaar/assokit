-- Migration 00016 : table login_magic_tokens (M-ASSOKIT-DCR-2).
-- Magic link login zéro-password : token random hex 32 bytes envoyé par email,
-- expires 15min, used_at une fois consommé. Rate-limit 3/15min/IP au handler.

-- +goose Up
CREATE TABLE IF NOT EXISTS login_magic_tokens (
  token       TEXT PRIMARY KEY,
  email       TEXT NOT NULL,
  user_id     TEXT REFERENCES users(id),
  return_url  TEXT NOT NULL DEFAULT '',
  expires_at  TEXT NOT NULL,
  used_at     TEXT,
  ip_hash     TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_magic_tokens_email_unused
  ON login_magic_tokens(email, expires_at)
  WHERE used_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS login_magic_tokens;
