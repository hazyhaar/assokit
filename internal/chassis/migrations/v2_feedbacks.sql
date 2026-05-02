-- Migration v2 — M-ASSOKIT-FEEDBACK-F1-SCHEMA-MIGRATION-V2-FEEDBACKS-TABLE-ANON
-- Table feedbacks : anonyme strict (pas de user_id ni email), message libre,
-- rattaché à la page d'origine. RGPD : IP pseudo-anonymisée via SHA256+secret.
CREATE TABLE feedbacks (
  id           TEXT PRIMARY KEY,
  page_url     TEXT NOT NULL,
  page_title   TEXT NOT NULL DEFAULT '',
  message      TEXT NOT NULL CHECK (length(message) BETWEEN 5 AND 2000),
  ip_hash      TEXT NOT NULL DEFAULT '',
  user_agent   TEXT NOT NULL DEFAULT '',
  locale       TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending','triaged','closed','spam')),
  admin_note   TEXT NOT NULL DEFAULT '',
  triaged_by   TEXT REFERENCES users(id),
  triaged_at   TEXT,
  created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  collected_at TEXT
) STRICT;

CREATE INDEX idx_feedbacks_status    ON feedbacks(status, created_at);
CREATE INDEX idx_feedbacks_collected ON feedbacks(collected_at) WHERE collected_at IS NULL;
