-- +goose Up
CREATE TABLE IF NOT EXISTS oauth_auth_requests (
    id                    TEXT PRIMARY KEY,
    client_id             TEXT NOT NULL,
    user_id               TEXT NOT NULL DEFAULT '',
    scopes                TEXT NOT NULL DEFAULT '[]',
    redirect_uri          TEXT NOT NULL,
    nonce                 TEXT NOT NULL DEFAULT '',
    state                 TEXT NOT NULL DEFAULT '',
    code_challenge        TEXT NOT NULL DEFAULT '',
    code_challenge_method TEXT NOT NULL DEFAULT '',
    response_type         TEXT NOT NULL DEFAULT 'code',
    auth_time             TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    done                  INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id          TEXT PRIMARY KEY,
    client_secret_hash TEXT NOT NULL DEFAULT '',
    redirect_uris      TEXT NOT NULL DEFAULT '[]',
    grant_types        TEXT NOT NULL DEFAULT '["authorization_code"]',
    scopes             TEXT NOT NULL DEFAULT '[]',
    owner_user_id      TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS oauth_authcodes (
    code            TEXT PRIMARY KEY,
    auth_req_id     TEXT NOT NULL,
    client_id       TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    scopes          TEXT NOT NULL DEFAULT '[]',
    redirect_uri    TEXT NOT NULL,
    expires_at      TEXT NOT NULL,
    used_at         TEXT
) STRICT;

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                 TEXT PRIMARY KEY,
    access_token_hash  TEXT UNIQUE,
    refresh_token_hash TEXT UNIQUE,
    client_id          TEXT NOT NULL,
    user_id            TEXT NOT NULL,
    scopes             TEXT NOT NULL DEFAULT '[]',
    expires_at         TEXT NOT NULL,
    revoked_at         TEXT
) STRICT;

CREATE TABLE IF NOT EXISTS oauth_external_links (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    provider    TEXT NOT NULL,
    external_id TEXT NOT NULL,
    email       TEXT NOT NULL DEFAULT '',
    linked_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (provider, external_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user    ON oauth_tokens(user_id, revoked_at);
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_access  ON oauth_tokens(access_token_hash)  WHERE access_token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_refresh ON oauth_tokens(refresh_token_hash) WHERE refresh_token_hash IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_oauth_tokens_refresh;
DROP INDEX IF EXISTS idx_oauth_tokens_access;
DROP INDEX IF EXISTS idx_oauth_tokens_user;
DROP TABLE IF EXISTS oauth_external_links;
DROP TABLE IF EXISTS oauth_tokens;
DROP TABLE IF EXISTS oauth_authcodes;
DROP TABLE IF EXISTS oauth_clients;
DROP TABLE IF EXISTS oauth_auth_requests;
