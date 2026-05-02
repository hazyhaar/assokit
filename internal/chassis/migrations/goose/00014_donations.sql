-- Migration 00014 : table donations (M-ASSOKIT-SPRINT3-S2).
-- Mapping payment HelloAsso → row interne. UNIQUE(helloasso_payment_id) pour idempotency.
-- RGPD : donor_email peut être effacé (SET ''), donor_name supprimé sur demande, row gardée.

-- +goose Up
CREATE TABLE IF NOT EXISTS donations (
  id                    TEXT PRIMARY KEY,
  helloasso_payment_id  TEXT NOT NULL UNIQUE,
  helloasso_form_slug   TEXT NOT NULL DEFAULT '',
  helloasso_form_type   TEXT NOT NULL DEFAULT '',
  amount_cents          INTEGER NOT NULL,
  currency              TEXT NOT NULL DEFAULT 'EUR',
  user_id               TEXT REFERENCES users(id),
  donor_email           TEXT NOT NULL DEFAULT '',
  donor_name            TEXT NOT NULL DEFAULT '',
  payment_status        TEXT NOT NULL
                        CHECK (payment_status IN ('pending','authorized','paid','refunded','failed')),
  paid_at               TEXT,
  refunded_at           TEXT,
  raw_event_id          TEXT NOT NULL,
  created_at            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_donations_user
  ON donations(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_donations_paid_at
  ON donations(paid_at DESC) WHERE paid_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_donations_status
  ON donations(payment_status);

-- +goose Down
DROP TABLE IF EXISTS donations;
