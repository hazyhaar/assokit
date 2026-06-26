-- +goose Up
-- Journal de terrain de l'éleveur/preneur (V3b). Un membre preneur consigne soit
-- un entretien réalisé sur une parcelle mise à disposition (clôture, point d'eau,
-- débroussaillage…), soit un problème observé sur le terrain (gibier, divagation,
-- accès, danger…). Les deux catégories partagent une même table, distinguées par la
-- colonne kind : ce sont deux variantes du même geste de saisie léger (patron
-- pkg/mutation), sans workflow de traitement (suivi/résolution différés it.2).
--
-- Le rattachement à la parcelle est une référence textuelle libre (parcelle_ref)
-- plutôt qu'une FK dure : le membre désigne une parcelle par sa référence cadastrale
-- telle qu'il la connaît, qui n'est pas toujours enregistrée au registre. Le
-- rattachement au membre déclarant est une FK ON DELETE CASCADE (la trace disparaît
-- avec le compte, cohérent RGPD). La table est consultable côté administration
-- (toutes catégories confondues) pour le suivi par le bureau.
CREATE TABLE IF NOT EXISTS field_logs (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK(kind IN ('entretien','probleme')),
  category     TEXT NOT NULL CHECK(
                 (kind='entretien' AND category IN ('cloture','eau','debroussaillage','autre'))
                 OR
                 (kind='probleme' AND category IN ('gibier','divagation','acces','danger','autre'))),
  parcelle_ref TEXT NOT NULL DEFAULT '',
  event_date   TEXT NOT NULL DEFAULT '',
  details      TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_field_logs_user ON field_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_field_logs_kind ON field_logs(kind);

-- +goose Down
DROP INDEX IF EXISTS idx_field_logs_kind;
DROP INDEX IF EXISTS idx_field_logs_user;
DROP TABLE IF EXISTS field_logs;
