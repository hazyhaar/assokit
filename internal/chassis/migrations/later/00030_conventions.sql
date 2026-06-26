-- +goose Up
-- Convention de pâturage AFP (association foncière pastorale). Une convention
-- met une ou plusieurs parcelles à disposition d'un membre preneur, pour une
-- durée donnée et selon un statut de cycle de vie (brouillon, active, révoquée).
-- Le contenu textuel des clauses n'est PAS stocké ici : il est produit à la
-- génération par un fournisseur de clauses injecté à la bordure (interface
-- ClauseProvider). Aucun calcul de loyer ni signature électronique à ce stade
-- (différé it.2). La table de liaison rattache les parcelles à la convention.
CREATE TABLE IF NOT EXISTS conventions (
  id          TEXT PRIMARY KEY,
  preneur_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  duree_mois  INTEGER NOT NULL DEFAULT 60 CHECK(duree_mois > 0),
  statut      TEXT NOT NULL DEFAULT 'brouillon' CHECK(statut IN ('brouillon','active','revoquee')),
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE TABLE IF NOT EXISTS convention_parcelles (
  convention_id TEXT NOT NULL REFERENCES conventions(id) ON DELETE CASCADE,
  parcelle_id   TEXT NOT NULL REFERENCES parcelles(id) ON DELETE CASCADE,
  PRIMARY KEY(convention_id, parcelle_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_conventions_preneur          ON conventions(preneur_id);
CREATE INDEX IF NOT EXISTS idx_convention_parcelles_parcelle ON convention_parcelles(parcelle_id);

-- +goose Down
DROP INDEX IF EXISTS idx_convention_parcelles_parcelle;
DROP INDEX IF EXISTS idx_conventions_preneur;
DROP TABLE IF EXISTS convention_parcelles;
DROP TABLE IF EXISTS conventions;
