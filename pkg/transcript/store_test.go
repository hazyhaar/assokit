package transcript_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hazyhaar/assokit/pkg/transcript"
	_ "modernc.org/sqlite"
)

// newTestDB monte le schéma minimal (users + salon + transcript + transcript_segment)
// avec foreign_keys(1) en mémoire, à l'image de pkg/connectors/livekit/integration_configure_test.go.
func newTestDB(t *testing.T) *sql.DB {
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

func TestRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Données parentes requises par les FK.
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id) VALUES('u1')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO salon(id,name,slug,creator_id) VALUES('s1','Réunion hebdo','reunion-hebdo','u1')`); err != nil {
		t.Fatalf("insert salon: %v", err)
	}

	store := &transcript.Store{DB: db}

	// Créer un transcript.
	tr, err := store.CreateTranscript(ctx, "s1")
	if err != nil {
		t.Fatalf("CreateTranscript: %v", err)
	}
	if tr.Status != "pending" {
		t.Errorf("status = %q, want pending", tr.Status)
	}
	if tr.SalonID != "s1" {
		t.Errorf("salon_id = %q, want s1", tr.SalonID)
	}

	// Ajouter deux segments (ordre inversé t0_ms pour vérifier le tri).
	seg2, err := store.AddSegment(ctx, tr.ID, "Alice", 2000, 4000, "Bonjour.")
	if err != nil {
		t.Fatalf("AddSegment seg2: %v", err)
	}
	seg1, err := store.AddSegment(ctx, tr.ID, "Bob", 500, 1800, "Allo ?")
	if err != nil {
		t.Fatalf("AddSegment seg1: %v", err)
	}
	_ = seg2

	// Segments triés par t0_ms croissant.
	segs, err := store.Segments(ctx, tr.ID)
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(segs))
	}
	if segs[0].ID != seg1.ID {
		t.Errorf("segs[0] = %s, want seg1 (%s)", segs[0].ID, seg1.ID)
	}
	if segs[0].T0ms != 500 || segs[1].T0ms != 2000 {
		t.Errorf("t0_ms order wrong: %d %d", segs[0].T0ms, segs[1].T0ms)
	}

	// ListBySalon retourne le transcript.
	list, err := store.ListBySalon(ctx, "s1")
	if err != nil {
		t.Fatalf("ListBySalon: %v", err)
	}
	if len(list) != 1 || list[0].ID != tr.ID {
		t.Errorf("ListBySalon = %v", list)
	}

	// GetTranscript.
	got, err := store.GetTranscript(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if got.ID != tr.ID {
		t.Errorf("GetTranscript id = %s, want %s", got.ID, tr.ID)
	}

	// SetStatus done positionne ended_at.
	if err := store.SetStatus(ctx, tr.ID, "done"); err != nil {
		t.Fatalf("SetStatus done: %v", err)
	}
	got, err = store.GetTranscript(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTranscript after SetStatus: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("status = %q, want done", got.Status)
	}
	if !got.EndedAt.Valid {
		t.Error("ended_at doit être non-NULL après SetStatus('done')")
	}
}

func TestFKViolation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	store := &transcript.Store{DB: db}

	// AddSegment sur un transcript inexistant doit échouer (FK violation).
	_, err := store.AddSegment(ctx, "transcript-fantome", "Alice", 0, 1000, "Texte")
	if err == nil {
		t.Fatal("AddSegment sans transcript parent : attendu une erreur FK, obtenu nil")
	}
}

func TestCascadeDelete(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO users(id) VALUES('u2')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO salon(id,name,slug,creator_id) VALUES('s2','Demo','demo','u2')`); err != nil {
		t.Fatalf("insert salon: %v", err)
	}

	store := &transcript.Store{DB: db}
	tr, _ := store.CreateTranscript(ctx, "s2")
	_, _ = store.AddSegment(ctx, tr.ID, "X", 0, 500, "Hop")

	// Supprimer le salon → cascade vers transcript → cascade vers segment.
	if _, err := db.ExecContext(ctx, `DELETE FROM salon WHERE id='s2'`); err != nil {
		t.Fatalf("delete salon: %v", err)
	}

	// Vérifier que transcript et segments sont disparus.
	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript WHERE id=?`, tr.ID).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("transcript non supprimé après DELETE salon")
	}
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_segment WHERE transcript_id=?`, tr.ID).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("segment non supprimé après DELETE salon")
	}
}
