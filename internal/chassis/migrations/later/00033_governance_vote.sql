-- +goose Up
-- Gouvernance associative — décision : vote simple (V2c). Deux registres complètent
-- le socle réunion (V2a, migration 00031) et la présence/représentation (V2b,
-- migration 00032) sans toucher aux tables assemblies/convocations/mandates/attendance :
--   - resolutions : questions soumises au vote d'une assemblée. Une résolution a un
--                   cycle ouvert (open) puis clos (closed). Le scrutin n'accepte de
--                   votes que pendant qu'il est ouvert (garde applicative).
--   - votes       : bulletins individuels. Un vote par couple (résolution, votant) —
--                   UNIQUE(resolution_id, voter_user_id) interdit le double vote. Le
--                   choix est l'un de {pour, contre, abstention} (CHECK). Le vote est
--                   un acte enregistré : ni écrasement ni no-op silencieux.
-- Aucun vote pondéré aux apports, vote par procuration ni procès-verbal à ce stade.
CREATE TABLE IF NOT EXISTS resolutions (
  id          TEXT PRIMARY KEY,
  assembly_id TEXT NOT NULL REFERENCES assemblies(id) ON DELETE CASCADE,
  label       TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS votes (
  id            TEXT PRIMARY KEY,
  resolution_id TEXT NOT NULL REFERENCES resolutions(id) ON DELETE CASCADE,
  voter_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  choice        TEXT NOT NULL CHECK (choice IN ('pour', 'contre', 'abstention')),
  cast_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(resolution_id, voter_user_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_resolutions_assembly ON resolutions(assembly_id);
CREATE INDEX IF NOT EXISTS idx_votes_resolution     ON votes(resolution_id);

-- +goose Down
DROP INDEX IF EXISTS idx_votes_resolution;
DROP INDEX IF EXISTS idx_resolutions_assembly;
DROP TABLE IF EXISTS votes;
DROP TABLE IF EXISTS resolutions;
