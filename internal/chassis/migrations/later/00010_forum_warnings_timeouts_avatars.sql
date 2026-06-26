-- +goose Up
-- M-ASSOKIT-AUDIT-FIX-3 : tables backing pour les 3 stubs MCP (forum.user.warn,
-- forum.user.timeout, profile.avatar_upload) qui retournaient OK sans toucher la DB.
CREATE TABLE forum_warnings (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reason     TEXT NOT NULL,
  issued_by  TEXT NOT NULL REFERENCES users(id),
  issued_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;
CREATE INDEX idx_forum_warnings_user ON forum_warnings(user_id, issued_at);

CREATE TABLE forum_timeouts (
  user_id     TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  reason      TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  issued_by   TEXT NOT NULL REFERENCES users(id),
  issued_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE user_avatars (
  user_id      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  avatar_url   TEXT NOT NULL,
  uploaded_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

-- +goose Down
DROP TABLE user_avatars;
DROP TABLE forum_timeouts;
DROP TABLE forum_warnings;
