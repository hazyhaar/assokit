-- +goose Up
-- Socle de publication réseaux sociaux (G0) — planification LOCALE au DB cœur de
-- l'instance, drainée par un worker à ticker (pkg/connectors/social). Aucune
-- dépendance au bus jobs.db horos55 : la file de publication vit ici, dans la DB
-- de l'instance, et le worker en est le seul écrivain (single-writer).

-- File de publication. media_paths et networks sont des tableaux JSON (chemins
-- médias locaux, ids de réseaux cibles). idempotency_key dédoublonne l'enqueue :
-- deux insertions de même clé ne créent qu'une entrée. state agrège l'avancement
-- (pending → sent quand tous les réseaux cibles ont été publiés, failed sur
-- épuisement des re-tentatives). La non-republication PAR RÉSEAU est portée par le
-- journal social_post_log, pas par cette colonne.
CREATE TABLE IF NOT EXISTS social_post_queue (
  id              TEXT PRIMARY KEY,
  content         TEXT NOT NULL DEFAULT '',
  media_paths     TEXT NOT NULL DEFAULT '[]',
  networks        TEXT NOT NULL DEFAULT '[]',
  scheduled_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  idempotency_key TEXT NOT NULL UNIQUE,
  state           TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sent','failed')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
) STRICT;

CREATE INDEX IF NOT EXISTS idx_social_post_queue_due
  ON social_post_queue(state, scheduled_at);

-- Journal d'idempotence et de coût par (post, réseau). Une ligne unique par paire
-- (post_id, network) : posée en 'publishing' AVANT l'appel Publish, promue à 'sent'
-- après succès (avec post_ref/url/coût), ou 'failed' sur échec re-tentable. Le
-- verrou de non-republication est : une paire en statut 'sent' n'est jamais
-- re-publiée. post_ref est l'id natif du post chez le provider.
CREATE TABLE IF NOT EXISTS social_post_log (
  id         TEXT PRIMARY KEY,
  post_id    TEXT NOT NULL REFERENCES social_post_queue(id) ON DELETE CASCADE,
  network    TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'publishing' CHECK(status IN ('publishing','sent','failed')),
  post_ref   TEXT NOT NULL DEFAULT '',
  post_url   TEXT NOT NULL DEFAULT '',
  cost_cents INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  -- UNIQUE(post_id, network) fournit déjà l'index de lecture du verrou
  -- d'idempotence (pas d'index supplémentaire nécessaire).
  UNIQUE(post_id, network)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS social_post_log;
DROP INDEX IF EXISTS idx_social_post_queue_due;
DROP TABLE IF EXISTS social_post_queue;
