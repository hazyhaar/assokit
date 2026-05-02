// CLAUDE:SUMMARY Store webhook_events — Insert idempotent + Claim/Mark pour worker drain (M-ASSOKIT-SPRINT2-S4).
// CLAUDE:WARN Insert ON CONFLICT(id) DO NOTHING : duplicate event_id retourne ErrDuplicate (le receiver répond 200 idempotent).
package webhooks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrDuplicate signale qu'un event_id existe déjà (idempotency hit).
var ErrDuplicate = errors.New("webhooks: event_id déjà reçu (duplicate)")

// Event représente une row webhook_events.
type Event struct {
	ID          string
	Provider    string
	EventType   string
	Payload     string
	Signature   string
	Status      string
	Attempts    int
	LastError   string
	ReceivedAt  time.Time
	NextRetryAt sql.NullTime
	ProcessedAt sql.NullTime
}

// Store wrap les opérations DB sur webhook_events.
type Store struct {
	DB *sql.DB
}

// Insert tente d'insérer un nouveau event. Retourne ErrDuplicate si event_id existe déjà.
// Caller doit alors marquer status='duplicate' sur la row existante via MarkDuplicate.
func (s *Store) Insert(ctx context.Context, e Event) error {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO webhook_events(id, provider, event_type, payload, signature, status)
		VALUES (?, ?, ?, ?, ?, 'pending')
		ON CONFLICT(id) DO NOTHING
	`, e.ID, e.Provider, e.EventType, e.Payload, e.Signature)
	if err != nil {
		return fmt.Errorf("webhooks.Insert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDuplicate
	}
	return nil
}

// MarkDuplicate met status='duplicate' sur une row existante (info audit).
// No-op si la row n'existe pas (cas race vraisemblable absent post-Insert).
func (s *Store) MarkDuplicate(ctx context.Context, eventID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE webhook_events SET status='duplicate' WHERE id = ? AND status='pending'`,
		eventID)
	return err
}

// ClaimPending sélectionne et marque processing N events pending dont next_retry_at est passé.
// Atomique via UPDATE...WHERE id IN (SELECT...).
func (s *Store) ClaimPending(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, provider, event_type, payload, signature, attempts, last_error
		FROM webhook_events
		WHERE status='pending'
		  AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP)
		ORDER BY received_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("webhooks.ClaimPending query: %w", err)
	}
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventType, &e.Payload, &e.Signature, &e.Attempts, &e.LastError); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()

	for _, e := range out {
		_, _ = s.DB.ExecContext(ctx,
			`UPDATE webhook_events SET status='processing' WHERE id = ? AND status='pending'`, e.ID)
	}
	return out, nil
}

// MarkProcessed pose status='processed' + processed_at=now.
func (s *Store) MarkProcessed(ctx context.Context, eventID string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE webhook_events SET status='processed', processed_at=CURRENT_TIMESTAMP WHERE id = ?`,
		eventID)
	return err
}

// MarkFailed incrémente attempts + last_error. Si attempts ≥ maxAttempts → status='failed' final.
// Sinon → status='pending' + next_retry_at calculé (backoff exponentiel : 1, 5, 30 min).
func (s *Store) MarkFailed(ctx context.Context, eventID string, errMsg string, maxAttempts int) error {
	var attempts int
	err := s.DB.QueryRowContext(ctx,
		`SELECT attempts FROM webhook_events WHERE id = ?`, eventID).Scan(&attempts)
	if err != nil {
		return fmt.Errorf("webhooks.MarkFailed read: %w", err)
	}
	attempts++
	if attempts >= maxAttempts {
		_, err := s.DB.ExecContext(ctx, `
			UPDATE webhook_events SET status='failed', attempts=?, last_error=?, processed_at=CURRENT_TIMESTAMP
			WHERE id = ?
		`, attempts, errMsg, eventID)
		return err
	}
	delay := backoffDelay(attempts)
	nextRetry := time.Now().UTC().Add(delay).Format("2006-01-02 15:04:05")
	_, err = s.DB.ExecContext(ctx, `
		UPDATE webhook_events SET status='pending', attempts=?, last_error=?, next_retry_at=?
		WHERE id = ?
	`, attempts, errMsg, nextRetry, eventID)
	return err
}

// backoffDelay : exponentiel, attempts 1=1min, 2=5min, 3=30min.
func backoffDelay(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 1 * time.Minute
	case 2:
		return 5 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// CountByStatus retourne le count par status (pour métriques + tests).
func (s *Store) CountByStatus(ctx context.Context, provider, status string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_events WHERE provider = ? AND status = ?`,
		provider, status).Scan(&n)
	return n, err
}
