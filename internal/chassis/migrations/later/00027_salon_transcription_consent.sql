-- +goose Up
-- transcription_consent : opt-in explicite de l'hôte autorisant la transcription (RGPD).
-- Sans ce flag à 1, le bot transcripteur (T3) ne démarre pas.
ALTER TABLE salon ADD COLUMN transcription_consent INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite STRICT ne supporte pas DROP COLUMN portable ; no-op au rollback.
