// CLAUDE:SUMMARY Tests gardiens Store webhooks — Insert idempotent, Claim/Mark/backoff (M-ASSOKIT-SPRINT2-S4).
package webhooks

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const webhookSchema = `
CREATE TABLE webhook_events (
	id TEXT PRIMARY KEY, provider TEXT NOT NULL, event_type TEXT NOT NULL,
	payload TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending'
		CHECK (status IN ('pending','processing','processed','failed','duplicate')),
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	next_retry_at TEXT,
	processed_at TEXT
);
`

func openWebhookDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(webhookSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestStore_InsertNewEventOK : Insert new id → 0 row à pending.
func TestStore_InsertNewEventOK(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	ev := Event{ID: "evt-1", Provider: "helloasso", EventType: "payment.completed", Payload: `{"amount":42}`}
	if err := s.Insert(context.Background(), ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	n, _ := s.CountByStatus(context.Background(), "helloasso", "pending")
	if n != 1 {
		t.Errorf("count pending = %d, attendu 1", n)
	}
}

// TestStore_InsertDuplicateReturnsErrDuplicate : 2× même id → ErrDuplicate.
func TestStore_InsertDuplicateReturnsErrDuplicate(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	ev := Event{ID: "evt-dup", Provider: "helloasso", EventType: "x", Payload: `{}`}
	if err := s.Insert(context.Background(), ev); err != nil {
		t.Fatalf("Insert 1: %v", err)
	}
	err := s.Insert(context.Background(), ev)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("Insert 2 err = %v, attendu ErrDuplicate", err)
	}
	// Marquer duplicate
	if err := s.MarkDuplicate(context.Background(), "evt-dup"); err != nil {
		t.Fatalf("MarkDuplicate: %v", err)
	}
	// La status DOIT rester "pending" car MarkDuplicate ne change que pending→duplicate
	// si la première insert a réussi (ce cas).
	n, _ := s.CountByStatus(context.Background(), "helloasso", "duplicate")
	if n != 1 {
		t.Errorf("count duplicate = %d, attendu 1 (1ère row passe à duplicate)", n)
	}
}

// TestStore_ClaimPendingMarksProcessing : Claim → status='processing' atomique.
func TestStore_ClaimPendingMarksProcessing(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	for _, id := range []string{"a", "b", "c"} {
		_ = s.Insert(context.Background(), Event{ID: id, Provider: "helloasso", EventType: "x", Payload: "{}"})
	}
	claimed, err := s.ClaimPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 3 {
		t.Errorf("claimed=%d, attendu 3", len(claimed))
	}
	n, _ := s.CountByStatus(context.Background(), "helloasso", "processing")
	if n != 3 {
		t.Errorf("count processing = %d, attendu 3", n)
	}
}

// TestStore_MarkProcessed : MarkProcessed → status='processed' + processed_at.
func TestStore_MarkProcessed(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	_ = s.Insert(context.Background(), Event{ID: "ok", Provider: "p", EventType: "x", Payload: "{}"})
	_, _ = s.ClaimPending(context.Background(), 10)
	if err := s.MarkProcessed(context.Background(), "ok"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	n, _ := s.CountByStatus(context.Background(), "p", "processed")
	if n != 1 {
		t.Errorf("count processed = %d, attendu 1", n)
	}
}

// TestStore_MarkFailedRetryPendingUntilMaxAttempts : 1ère fail → pending+attempt+next_retry, MaxAttempts → failed.
func TestStore_MarkFailedRetryPendingUntilMaxAttempts(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	_ = s.Insert(context.Background(), Event{ID: "f", Provider: "p", EventType: "x", Payload: "{}"})

	// 3 attempts → reste pending
	for i := 0; i < 3; i++ {
		_, _ = s.ClaimPending(context.Background(), 10)
		if err := s.MarkFailed(context.Background(), "f", "boom", 4); err != nil {
			t.Fatalf("MarkFailed iter %d: %v", i, err)
		}
	}
	var status string
	var attempts int
	db.QueryRow(`SELECT status, attempts FROM webhook_events WHERE id='f'`).Scan(&status, &attempts)
	if status != "pending" || attempts != 3 {
		t.Errorf("après 3 fails : status=%q attempts=%d, attendu pending+3", status, attempts)
	}

	// 4e fail → failed final
	_, _ = s.ClaimPending(context.Background(), 10)
	_ = s.MarkFailed(context.Background(), "f", "final boom", 4)
	db.QueryRow(`SELECT status, attempts FROM webhook_events WHERE id='f'`).Scan(&status, &attempts)
	if status != "failed" {
		t.Errorf("après 4 fails : status=%q, attendu failed", status)
	}
}

// TestStore_BackoffDelayProgression : 1→1min, 2→5min, 3+→30min.
func TestStore_BackoffDelayProgression(t *testing.T) {
	if d := backoffDelay(1); d.Minutes() != 1 {
		t.Errorf("backoff(1) = %v, attendu 1m", d)
	}
	if d := backoffDelay(2); d.Minutes() != 5 {
		t.Errorf("backoff(2) = %v, attendu 5m", d)
	}
	if d := backoffDelay(3); d.Minutes() != 30 {
		t.Errorf("backoff(3) = %v, attendu 30m", d)
	}
}
