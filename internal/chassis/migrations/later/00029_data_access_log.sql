-- +goose Up
-- Journal des accès nominatifs (couche RGPD transverse). Chaque ligne trace un
-- accès aux données personnelles d'une personne (subject) par un acteur (actor) :
-- consultation ou export. Table append-only : aucune mise à jour ni suppression
-- applicative — la conservation du journal lui-même est gérée séparément
-- (différé it.2). Le subject n'est pas une FK users : un accès peut porter sur
-- un sujet déjà supprimé (droit à l'effacement) sans perdre la trace de l'accès,
-- et le subject peut être d'une autre nature (parcelle, droit) que l'utilisateur.
CREATE TABLE IF NOT EXISTS data_access_log (
  id           TEXT PRIMARY KEY,
  user_id      TEXT,
  subject_kind TEXT NOT NULL,
  subject_id   TEXT NOT NULL,
  actor_id     TEXT NOT NULL,
  action       TEXT NOT NULL,
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_data_access_log_subject ON data_access_log(subject_kind, subject_id);
CREATE INDEX IF NOT EXISTS idx_data_access_log_user    ON data_access_log(user_id);
CREATE INDEX IF NOT EXISTS idx_data_access_log_actor   ON data_access_log(actor_id);

-- +goose Down
DROP INDEX IF EXISTS idx_data_access_log_actor;
DROP INDEX IF EXISTS idx_data_access_log_user;
DROP INDEX IF EXISTS idx_data_access_log_subject;
DROP TABLE IF EXISTS data_access_log;
