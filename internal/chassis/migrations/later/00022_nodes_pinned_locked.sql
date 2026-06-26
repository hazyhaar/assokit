-- +goose Up
-- Les actions registry forum.post.pin/unpin et forum.thread.lock/unlock ciblaient
-- des colonnes inexistantes (pinned, locked) → échec à l'exécution réelle (MCP/LLM),
-- violant l'invariant LLM-parité. On ajoute les colonnes manquantes (deux ADD COLUMN
-- séparés ; SQLite n'accepte qu'une colonne par ALTER TABLE ADD COLUMN).
ALTER TABLE nodes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN locked INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite STRICT ne supporte pas DROP COLUMN portable ; no-op au rollback.
