-- +goose Up
-- users.deactivate enregistre une raison (audit) ; la colonne manquait, l'action
-- échouait silencieusement (faux OK révélé par les gardiens S5).
ALTER TABLE users ADD COLUMN deactivation_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite STRICT ne supporte pas DROP COLUMN portable ; no-op au rollback.
