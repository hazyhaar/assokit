-- +goose Up
CREATE TABLE IF NOT EXISTS permissions (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS grades (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    system      INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS grade_permissions (
    grade_id      TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    granted_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (grade_id, permission_id)
) STRICT;

CREATE TABLE IF NOT EXISTS grade_inherits (
    child_id  TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
    parent_id TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
    PRIMARY KEY (child_id, parent_id),
    CHECK (child_id != parent_id)
) STRICT;

CREATE TABLE IF NOT EXISTS user_grades (
    user_id    TEXT NOT NULL,
    grade_id   TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
    assigned_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, grade_id)
) STRICT;

CREATE TABLE IF NOT EXISTS user_effective_permissions (
    user_id       TEXT NOT NULL,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, permission_id)
) STRICT;

CREATE TABLE IF NOT EXISTS rbac_audit (
    id         TEXT PRIMARY KEY,
    action     TEXT NOT NULL,
    actor_id   TEXT NOT NULL DEFAULT '',
    target_id  TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_user_grades_user ON user_grades(user_id);
CREATE INDEX IF NOT EXISTS idx_rbac_audit_created ON rbac_audit(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_rbac_audit_created;
DROP INDEX IF EXISTS idx_user_grades_user;
DROP TABLE IF EXISTS user_effective_permissions;
DROP TABLE IF EXISTS user_grades;
DROP TABLE IF EXISTS grade_inherits;
DROP TABLE IF EXISTS grade_permissions;
DROP TABLE IF EXISTS grades;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS rbac_audit;
