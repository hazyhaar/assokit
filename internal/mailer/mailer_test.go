package mailer_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/internal/chassis"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func loadOutbox(t *testing.T, db *sql.DB, id string) mailer.OutboxMsg {
	t.Helper()
	var msg mailer.OutboxMsg
	err := db.QueryRow(
		`SELECT id, to_addr, subject, body_text, body_html, attempts, retry_after
		 FROM email_outbox WHERE id=?`, id,
	).Scan(&msg.ID, &msg.ToAddr, &msg.Subject, &msg.BodyText, &msg.BodyHTML, &msg.Attempts, &msg.RetryAfter)
	if err != nil {
		t.Fatalf("loadOutbox: %v", err)
	}
	return msg
}

func TestEnqueueInsertsRow(t *testing.T) {
	db := newTestDB(t)
	m := &mailer.Mailer{DB: db}
	ctx := context.Background()

	if err := m.Enqueue(ctx, "test@example.com", "Sujet", "texte", "<p>html</p>"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM email_outbox WHERE status='pending'`).Scan(&count)
	if count != 1 {
		t.Errorf("attendu 1 row pending, got %d", count)
	}
}

// TestMailer_RunWorker_RetryExponential vérifie que ApplyBackoff incrémente attempts
// et pose retry_after dans le futur avec un délai d'au moins 30s.
func TestMailer_RunWorker_RetryExponential(t *testing.T) {
	db := newTestDB(t)
	m := &mailer.Mailer{DB: db, APIKey: "test", From: "contact@nps.org"}
	ctx := context.Background()

	m.Enqueue(ctx, "x@y.com", "Test", "body", "<p>html</p>")

	var msgID string
	db.QueryRow(`SELECT id FROM email_outbox WHERE status='pending'`).Scan(&msgID)
	msg := loadOutbox(t, db, msgID)

	// Premier échec : attempts=0 → backoff 30s
	m.ApplyBackoff(ctx, msg, "HTTP 500")

	updated := loadOutbox(t, db, msgID)
	if updated.Attempts != 1 {
		t.Errorf("après 1 échec, attempts want 1 got %d", updated.Attempts)
	}
	if !updated.RetryAfter.Valid {
		t.Fatal("retry_after doit être non nul après échec")
	}

	exp, err := time.Parse("2006-01-02 15:04:05", updated.RetryAfter.String)
	if err != nil {
		t.Fatalf("parse retry_after: %v", err)
	}
	// Délai minimum attendu : 25s (tolérance -5s pour la lenteur CI)
	if exp.Before(time.Now().Add(25 * time.Second)) {
		t.Errorf("retry_after devrait être ≥25s dans le futur, got %v", exp)
	}

	// Deuxième échec : attempts=1 → backoff 60s
	updated.Attempts = 1
	m.ApplyBackoff(ctx, updated, "HTTP 429")
	updated2 := loadOutbox(t, db, msgID)
	exp2, _ := time.Parse("2006-01-02 15:04:05", updated2.RetryAfter.String)
	// 60s > 30s — retry_after plus loin dans le futur
	if !exp2.After(exp) {
		t.Errorf("backoff 2e tentative (%v) devrait être > 1ere (%v)", exp2, exp)
	}
}

func TestMailer_DisabledWhenNoAPIKey(t *testing.T) {
	db := newTestDB(t)
	m := &mailer.Mailer{DB: db, APIKey: ""}
	ctx := context.Background()

	m.Enqueue(ctx, "x@y.com", "Test", "body", "<p>html</p>")

	var msgID string
	db.QueryRow(`SELECT id FROM email_outbox WHERE status='pending'`).Scan(&msgID)
	msg := loadOutbox(t, db, msgID)

	// SendNow avec APIKey vide = no-op sans erreur
	if err := m.SendNow(ctx, msg); err != nil {
		t.Errorf("SendNow sans APIKey devrait être no-op, got: %v", err)
	}
}
