-- +goose Up
-- Déclaration de mutation (V3a). Un propriétaire membre déclare une mutation
-- (vente, succession, autre) portant sur l'une de ses parcelles. La déclaration
-- est une trace consultable côté administration : elle n'opère aucun transfert de
-- droit ni de quote-part (workflow de traitement différé it.2). Le rattachement à
-- la parcelle est une référence cadastrale textuelle (parcelle_ref) plutôt qu'une
-- FK dure : une mutation peut viser une parcelle externe au registre (succession
-- d'un bien voisin), et la parcelle n'est pas toujours déjà enregistrée. Le
-- rattachement à l'utilisateur déclarant est une FK ON DELETE CASCADE (la trace
-- disparaît avec le compte, cohérent RGPD).
CREATE TABLE IF NOT EXISTS account_mutations (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parcelle_ref TEXT NOT NULL,
  type         TEXT NOT NULL CHECK(type IN ('vente','succession','autre')),
  details      TEXT NOT NULL DEFAULT '',
  declared_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  status       TEXT NOT NULL DEFAULT 'declaree' CHECK(status IN ('declaree','traitee'))
) STRICT;

CREATE INDEX IF NOT EXISTS idx_account_mutations_user ON account_mutations(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_account_mutations_user;
DROP TABLE IF EXISTS account_mutations;
