package transcription

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/pkg/transcript"
	_ "modernc.org/sqlite"
)

// newRetentionTestDB monte le schéma minimal avec FK activées.
func newRetentionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	for _, s := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY) STRICT`,
		`CREATE TABLE salon (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			slug         TEXT NOT NULL UNIQUE,
			creator_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			password_hash TEXT,
			visio_enabled INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			archived_at  TEXT
		) STRICT`,
		`CREATE TABLE transcript (
			id         TEXT PRIMARY KEY,
			salon_id   TEXT NOT NULL REFERENCES salon(id) ON DELETE CASCADE,
			started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			ended_at   TEXT,
			status     TEXT NOT NULL DEFAULT 'pending'
			           CHECK(status IN ('pending','recording','transcribing','done','failed'))
		) STRICT`,
		`CREATE TABLE transcript_segment (
			id            TEXT PRIMARY KEY,
			transcript_id TEXT NOT NULL REFERENCES transcript(id) ON DELETE CASCADE,
			speaker       TEXT NOT NULL,
			t0_ms         INTEGER NOT NULL,
			t1_ms         INTEGER NOT NULL,
			text          TEXT NOT NULL,
			created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		) STRICT`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// insertTranscriptAt insère un transcript avec started_at explicite (pour tester la TTL).
func insertTranscriptAt(t *testing.T, db *sql.DB, id, salonID string, startedAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO transcript(id, salon_id, started_at, status) VALUES(?,?,?,'done')`,
		id, salonID, startedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		t.Fatalf("insertTranscriptAt: %v", err)
	}
}

func insertSegment(t *testing.T, db *sql.DB, id, transcriptID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO transcript_segment(id,transcript_id,speaker,t0_ms,t1_ms,text,created_at)
		 VALUES(?,?,'alice',0,1000,'test','2026-01-01 00:00:00')`,
		id, transcriptID,
	)
	if err != nil {
		t.Fatalf("insertSegment: %v", err)
	}
}

// TestRetention_PurgeOldTranscripts vérifie que PurgeOldTranscripts supprime le vieux
// transcript (et ses segments via CASCADE) et conserve le récent.
func TestRetention_PurgeOldTranscripts(t *testing.T) {
	db := newRetentionTestDB(t)
	ctx := context.Background()

	// Données parentes.
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id) VALUES('u1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO salon(id,name,slug,creator_id) VALUES('s1','Réunion','reunion','u1')`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	ttl := 90 * 24 * time.Hour

	// Transcript vieux (200 jours) — doit être purgé.
	insertTranscriptAt(t, db, "old-tid", "s1", now.Add(-200*24*time.Hour))
	insertSegment(t, db, "seg-old-1", "old-tid")

	// Transcript récent (10 jours) — doit être conservé.
	insertTranscriptAt(t, db, "new-tid", "s1", now.Add(-10*24*time.Hour))
	insertSegment(t, db, "seg-new-1", "new-tid")

	store := &transcript.Store{DB: db}
	adapter := &RetentionStoreAdapter{S: store}
	w := &RetentionWorker{Store: adapter}

	n, err := w.PurgeOldTranscripts(ctx, ttl)
	if err != nil {
		t.Fatalf("PurgeOldTranscripts: %v", err)
	}
	if n != 1 {
		t.Errorf("attendu 1 supprimé, obtenu %d", n)
	}

	// Le vieux transcript doit avoir disparu.
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript WHERE id='old-tid'`).Scan(&count)
	if count != 0 {
		t.Error("old-tid aurait dû être supprimé")
	}

	// Ses segments aussi (CASCADE).
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_segment WHERE transcript_id='old-tid'`).Scan(&count)
	if count != 0 {
		t.Error("segments de old-tid auraient dû être supprimés par CASCADE")
	}

	// Le récent est intact.
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript WHERE id='new-tid'`).Scan(&count)
	if count != 1 {
		t.Error("new-tid aurait dû être conservé")
	}

	// Son segment aussi.
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_segment WHERE transcript_id='new-tid'`).Scan(&count)
	if count != 1 {
		t.Error("segment de new-tid aurait dû être conservé")
	}
}
