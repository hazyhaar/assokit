-- +goose Up
-- Demandes d'octroi de profil métier (O2). Un membre sollicite un grade métier
-- requestable ; la gouvernance (grade dédié) accepte ou refuse. Table dédiée,
-- distincte de demande_mads (mise à disposition parcellaire). Append-only sur
-- l'historique : le statut évolue, la ligne est conservée.
CREATE TABLE IF NOT EXISTS profile_requests (
  id        TEXT PRIMARY KEY,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  grade_id  TEXT NOT NULL,
  statut    TEXT NOT NULL DEFAULT 'soumise' CHECK(statut IN ('soumise','acceptee','refusee')),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_profile_requests_statut ON profile_requests(statut);
CREATE INDEX IF NOT EXISTS idx_profile_requests_user_grade ON profile_requests(user_id, grade_id);

-- +goose Down
DROP INDEX IF EXISTS idx_profile_requests_user_grade;
DROP INDEX IF EXISTS idx_profile_requests_statut;
DROP TABLE IF EXISTS profile_requests;