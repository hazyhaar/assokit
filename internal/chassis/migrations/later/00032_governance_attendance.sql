-- +goose Up
-- Gouvernance associative — présence et représentation (V2b). Deux registres
-- viennent compléter le socle réunion (V2a, migration 00031) sans toucher aux
-- tables assemblies/convocations :
--   - mandates   : pouvoirs de représentation. Un mandant (principal_user_id)
--                  confie à un mandataire (proxy_user_id) sa représentation à une
--                  assemblée. Au plus un mandat par mandant et par assemblée
--                  (UNIQUE(assembly_id, principal_user_id)).
--   - attendance : émargements. Présence d'un membre à une assemblée, horodatée et
--                  qualifiée par un canal (method). Un émargement unique par membre
--                  et par assemblée (UNIQUE(assembly_id, member_user_id)).
-- Le plafond du nombre de mandats portés par un mandataire est différé (it.2).
-- Aucun vote, résolution, clôture de scrutin ni procès-verbal à ce stade.
CREATE TABLE IF NOT EXISTS mandates (
  id                TEXT PRIMARY KEY,
  assembly_id       TEXT NOT NULL REFERENCES assemblies(id) ON DELETE CASCADE,
  principal_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  proxy_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at        TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(assembly_id, principal_user_id)
) STRICT;

CREATE TABLE IF NOT EXISTS attendance (
  id             TEXT PRIMARY KEY,
  assembly_id    TEXT NOT NULL REFERENCES assemblies(id) ON DELETE CASCADE,
  member_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  method         TEXT NOT NULL DEFAULT 'manual',
  signed_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(assembly_id, member_user_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_mandates_assembly   ON mandates(assembly_id);
CREATE INDEX IF NOT EXISTS idx_mandates_proxy      ON mandates(proxy_user_id);
CREATE INDEX IF NOT EXISTS idx_attendance_assembly ON attendance(assembly_id);

-- +goose Down
DROP INDEX IF EXISTS idx_attendance_assembly;
DROP INDEX IF EXISTS idx_mandates_proxy;
DROP INDEX IF EXISTS idx_mandates_assembly;
DROP TABLE IF EXISTS attendance;
DROP TABLE IF EXISTS mandates;
