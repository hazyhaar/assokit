-- +goose Up
-- Registre parcellaire AFP (association foncière pastorale). Une parcelle est
-- une référence cadastrale (commune, section, numéro) avec une surface et un
-- statut de mise à disposition. Les droits relient une parcelle à un membre
-- avec la nature de son droit. Aucune logique de vote ni de loyer ici : la
-- nature du droit est seulement stockée.
CREATE TABLE IF NOT EXISTS parcelles (
  id               TEXT PRIMARY KEY,
  commune_code     TEXT NOT NULL,
  section          TEXT NOT NULL,
  numero_parcelle  TEXT NOT NULL,
  surface_m2       INTEGER NOT NULL DEFAULT 0,
  statut_mad       TEXT NOT NULL DEFAULT 'libre' CHECK(statut_mad IN ('libre','mise_a_disposition')),
  created_at       TEXT DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(commune_code, section, numero_parcelle)
) STRICT;

CREATE TABLE IF NOT EXISTS parcelle_droits (
  id              TEXT PRIMARY KEY,
  parcelle_id     TEXT NOT NULL REFERENCES parcelles(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  nature_du_droit TEXT NOT NULL CHECK(nature_du_droit IN ('pleine_propriete','usufruit','nue_propriete','occupant')),
  created_at      TEXT DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(parcelle_id, user_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_parcelle_droits_user     ON parcelle_droits(user_id);
CREATE INDEX IF NOT EXISTS idx_parcelle_droits_parcelle ON parcelle_droits(parcelle_id);

-- +goose Down
DROP INDEX IF EXISTS idx_parcelle_droits_parcelle;
DROP INDEX IF EXISTS idx_parcelle_droits_user;
DROP TABLE IF EXISTS parcelle_droits;
DROP TABLE IF EXISTS parcelles;
