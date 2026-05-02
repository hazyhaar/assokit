-- +goose Up
-- M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW : tokens de reset password.
-- - token : secret hex 48 chars (24 bytes urandom).
-- - expires_at : 1h après création (court pour limiter window vol).
-- - used_at : single-use (NULL = pas encore consommé, NOT NULL = invalidé).
-- - created_ip_hash : SHA256(IP + COOKIE_SECRET) pour audit, RGPD safe.
CREATE TABLE password_reset_tokens (
  token            TEXT PRIMARY KEY,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at       TEXT NOT NULL,
  used_at          TEXT,
  created_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_ip_hash  TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX idx_password_reset_user ON password_reset_tokens(user_id, expires_at);
CREATE INDEX idx_password_reset_expires ON password_reset_tokens(expires_at) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE password_reset_tokens;
