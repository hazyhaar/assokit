-- +goose Up
-- Demande de mise à disposition (V3c). Un membre preneur sollicite la mise à
-- disposition d'une parcelle (référence cadastrale souhaitée, objet/usage,
-- détails). La demande est une trace consultable côté administration : elle
-- n'opère aucun transfert et ne crée aucune convention par elle-même. Le passage
-- de la demande au statut « convertie » accompagne la création d'une convention
-- par l'administration (parcours guidé de conversion). Le rattachement à la
-- parcelle est une référence cadastrale textuelle (parcelle_ref) plutôt qu'une FK
-- dure : la parcelle souhaitée n'est pas toujours déjà enregistrée au registre, et
-- une demande peut viser une parcelle externe (patron account_mutations). Le
-- rattachement au membre demandeur est une FK ON DELETE CASCADE (la trace
-- disparaît avec le compte, cohérent RGPD).
CREATE TABLE IF NOT EXISTS demande_mads (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parcelle_ref TEXT NOT NULL,
  objet        TEXT NOT NULL,
  details      TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  statut       TEXT NOT NULL DEFAULT 'soumise' CHECK(statut IN ('soumise','acceptee','refusee','convertie'))
) STRICT;

CREATE INDEX IF NOT EXISTS idx_demande_mads_user ON demande_mads(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_demande_mads_user;
DROP TABLE IF EXISTS demande_mads;
