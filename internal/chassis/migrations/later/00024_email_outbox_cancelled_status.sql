-- +goose Up
-- mailer.outbox.cancel doit pouvoir passer un email à 'cancelled', valeur
-- absente du CHECK initial ('pending','sent','failed'). SQLite STRICT n'offre
-- pas d'ALTER ... CHECK portable : on reconstruit la table avec le CHECK élargi
-- puis on recopie les lignes existantes (table-rebuild canonique SQLite).
CREATE TABLE email_outbox_new (
  id          TEXT PRIMARY KEY,
  to_addr     TEXT NOT NULL,
  subject     TEXT NOT NULL,
  body_text   TEXT NOT NULL DEFAULT '',
  body_html   TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending'
              CHECK(status IN ('pending','sent','failed','cancelled')),
  attempts    INTEGER NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT '',
  retry_after TEXT,
  created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  sent_at     TEXT
) STRICT;

INSERT INTO email_outbox_new
  (id, to_addr, subject, body_text, body_html, status, attempts, last_error, retry_after, created_at, sent_at)
SELECT
  id, to_addr, subject, body_text, body_html, status, attempts, last_error, retry_after, created_at, sent_at
FROM email_outbox;

DROP TABLE email_outbox;
ALTER TABLE email_outbox_new RENAME TO email_outbox;

CREATE INDEX idx_outbox_status ON email_outbox(status, created_at) WHERE status = 'pending';

-- +goose Down
-- Rétablir le CHECK restreint impliquerait un rebuild inverse ; no-op au rollback
-- (cohérent avec la doctrine STRICT du projet : pas de DROP COLUMN/CHECK portable).
