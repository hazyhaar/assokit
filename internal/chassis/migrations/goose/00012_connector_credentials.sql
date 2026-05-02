-- Migration 00012 : table connector_credentials chiffrée AES-256-GCM (M-ASSOKIT-SPRINT2-S2).
-- encrypted_value contient nonce|ciphertext|tag concaténés. Le master key est en env NPS_MASTER_KEY.
-- Les credentials sensibles (client_secret, api_key, webhook_signing_secret) sont stockés ici,
-- les params non-sensibles (sandbox_mode, callback_url) restent dans connectors.config_json.

-- +goose Up
CREATE TABLE IF NOT EXISTS connector_credentials (
  connector_id    TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
  key_name        TEXT NOT NULL,
  encrypted_value BLOB NOT NULL,
  set_at          TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  set_by          TEXT REFERENCES users(id),
  rotated_at      TEXT,
  PRIMARY KEY (connector_id, key_name)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS connector_credentials;
