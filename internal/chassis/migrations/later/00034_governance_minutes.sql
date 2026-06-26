-- +goose Up
-- Gouvernance associative — trace : procès-verbal et registre (V2d). Deux registres
-- append-only complètent la décision (V2c, migration 00033) sans toucher aux tables
-- assemblies/convocations/mandates/attendance/resolutions/votes :
--   - deliberations : entrées de registre. Une délibération FIGE le dépouillement d'une
--                     résolution close (pour/contre/abstention/total en colonnes, jamais
--                     recalculé). UNIQUE(resolution_id) : une seule consignation par
--                     résolution ; une seconde tentative est rejetée, jamais écrasée.
--   - minutes       : procès-verbal d'une assemblée (snapshot texte immuable).
--                     UNIQUE(assembly_id) : un PV par AG ; une régénération est rejetée.
-- Les deux tables sont append-only : aucune voie applicative ne les met à jour ni ne les
-- supprime. Aucune approbation, diffusion par courriel ni vote pondéré à ce stade.
--
-- FK ON DELETE RESTRICT (et non CASCADE) : une délibération et un procès-verbal sont des
-- traces IMMUABLES à valeur probante. La suppression d'une assemblée ou d'une résolution
-- qui porte une trace consignée est donc INTERDITE par la base (RESTRICT), pour que la
-- trace ne puisse jamais disparaître silencieusement par effet de bord d'une suppression
-- en amont. Détruire une AG/résolution déjà tracée exige de retirer d'abord la trace, ce
-- qu'aucune voie applicative n'autorise — la trace est donc protégée de bout en bout.
CREATE TABLE IF NOT EXISTS deliberations (
  id            TEXT PRIMARY KEY,
  assembly_id   TEXT NOT NULL REFERENCES assemblies(id) ON DELETE RESTRICT,
  resolution_id TEXT NOT NULL REFERENCES resolutions(id) ON DELETE RESTRICT,
  label         TEXT NOT NULL,
  pour          INTEGER NOT NULL,
  contre        INTEGER NOT NULL,
  abstention    INTEGER NOT NULL,
  total         INTEGER NOT NULL,
  recorded_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(resolution_id)
) STRICT;

CREATE TABLE IF NOT EXISTS minutes (
  id           TEXT PRIMARY KEY,
  assembly_id  TEXT NOT NULL REFERENCES assemblies(id) ON DELETE RESTRICT,
  body         TEXT NOT NULL,
  generated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(assembly_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_deliberations_assembly ON deliberations(assembly_id);

-- +goose Down
DROP INDEX IF EXISTS idx_deliberations_assembly;
DROP TABLE IF EXISTS minutes;
DROP TABLE IF EXISTS deliberations;
