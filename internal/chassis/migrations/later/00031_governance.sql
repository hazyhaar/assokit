-- +goose Up
-- Gouvernance associative — socle réunion (V2a). Une assemblée générale (AG) porte
-- un ordre du jour et une date programmée, et traverse un cycle de vie réduit :
-- brouillon (drafting), convoquée (convoked), annulée (cancelled). La convocation
-- consigne, par adhérent actif effectivement contacté, l'horodatage d'envoi du
-- courriel (table convocations). Aucun mandat, émargement, quorum, vote, ni
-- procès-verbal à ce stade (lots ultérieurs).
CREATE TABLE IF NOT EXISTS assemblies (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  agenda       TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'drafting' CHECK(status IN ('drafting','convoked','cancelled')),
  scheduled_at TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS convocations (
  id          TEXT PRIMARY KEY,
  assembly_id TEXT NOT NULL REFERENCES assemblies(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  sent_at     TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_convocations_assembly ON convocations(assembly_id);
CREATE INDEX IF NOT EXISTS idx_convocations_user     ON convocations(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_convocations_user;
DROP INDEX IF EXISTS idx_convocations_assembly;
DROP TABLE IF EXISTS convocations;
DROP TABLE IF EXISTS assemblies;
