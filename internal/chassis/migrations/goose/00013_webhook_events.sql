-- Migration 00013 : table webhook_events (M-ASSOKIT-SPRINT2-S4).
-- Persiste les événements webhook reçus de connecteurs tiers (HelloAsso, Stripe, etc.).
-- Idempotency-key = id (event_id du provider). Worker drain async traite les pending.

-- +goose Up
CREATE TABLE IF NOT EXISTS webhook_events (
  id            TEXT PRIMARY KEY,
  provider      TEXT NOT NULL,
  event_type    TEXT NOT NULL,
  payload       TEXT NOT NULL,
  signature     TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','processing','processed','failed','duplicate')),
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT NOT NULL DEFAULT '',
  received_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  next_retry_at TEXT,
  processed_at  TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_webhook_events_pending
  ON webhook_events(provider, status, received_at)
  WHERE status='pending';

-- +goose Down
DROP TABLE IF EXISTS webhook_events;
