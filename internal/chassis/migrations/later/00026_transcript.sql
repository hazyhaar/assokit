-- +goose Up
-- Transcripts : enregistrements audio d'un salon, découpés en segments.
CREATE TABLE IF NOT EXISTS transcript (
  id          TEXT PRIMARY KEY,
  salon_id    TEXT NOT NULL REFERENCES salon(id) ON DELETE CASCADE,
  started_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ended_at    TEXT,
  status      TEXT NOT NULL DEFAULT 'pending'
              CHECK(status IN ('pending','recording','transcribing','done','failed'))
) STRICT;

CREATE TABLE IF NOT EXISTS transcript_segment (
  id            TEXT PRIMARY KEY,
  transcript_id TEXT NOT NULL REFERENCES transcript(id) ON DELETE CASCADE,
  speaker       TEXT NOT NULL,
  t0_ms         INTEGER NOT NULL,
  t1_ms         INTEGER NOT NULL,
  text          TEXT NOT NULL,
  created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;

CREATE INDEX IF NOT EXISTS idx_transcript_salon ON transcript(salon_id, started_at);
CREATE INDEX IF NOT EXISTS idx_segment_transcript ON transcript_segment(transcript_id, t0_ms);

-- +goose Down
DROP INDEX IF EXISTS idx_segment_transcript;
DROP INDEX IF EXISTS idx_transcript_salon;
DROP TABLE IF EXISTS transcript_segment;
DROP TABLE IF EXISTS transcript;
