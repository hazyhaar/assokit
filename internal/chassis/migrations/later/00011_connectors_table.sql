-- Migration 00011 : table connectors (M-ASSOKIT-SPRINT2-S1).
-- Stocke la configuration non-sensible des connecteurs tiers (HelloAsso, Stripe, etc.).
-- Credentials sensibles sont stockés séparément (table connector_credentials chiffrée AES-GCM, livrée S2-2).

-- +goose Up
CREATE TABLE IF NOT EXISTS connectors (
  id            TEXT PRIMARY KEY,
  enabled       INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
  config_json   TEXT NOT NULL DEFAULT '{}',
  configured_at TEXT,
  configured_by TEXT REFERENCES users(id),
  created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS connectors;
