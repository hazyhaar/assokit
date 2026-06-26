-- +goose Up
-- Cotisations / adhésions (memberships). Une adhésion = période + montant +
-- statut, liée à un membre. Historisable (un membre a N adhésions dans le temps).
-- Montant en centimes (INTEGER) — jamais de float monétaire.
CREATE TABLE IF NOT EXISTS memberships (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  period_start TEXT NOT NULL,
  period_end   TEXT NOT NULL,
  amount_cents INTEGER NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','expired','cancelled')),
  note         TEXT NOT NULL DEFAULT '',
  created_at   TEXT DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_memberships_user   ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_memberships_status ON memberships(status);

-- +goose Down
DROP INDEX IF EXISTS idx_memberships_status;
DROP INDEX IF EXISTS idx_memberships_user;
DROP TABLE IF EXISTS memberships;
