// CLAUDE:SUMMARY Outbox SMTP via Resend (HTTP port 443) — Enqueue, SendNow, RunWorker avec backoff exponentiel.
package mailer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Mailer gère l'envoi d'emails. Deux backends supportés :
//   - SMTPS (ex OVH Email Pro, port 465 implicit TLS) si SMTPHost est défini
//   - Resend (HTTP /emails) si APIKey est défini
//
// Si les deux sont vides → mailer désactivé, outbox accumule en status=pending,
// drainé au prochain restart avec config valide. Si les deux définis → SMTPS prime.
type Mailer struct {
	DB       *sql.DB
	APIKey   string // Resend API key (fallback si SMTPHost vide)
	SMTPHost string // ex "ssl0.ovh.net" — si défini, backend SMTPS port 465
	SMTPPort int    // 0 → default 465
	SMTPUser string // ex "contact@example.org"
	SMTPPass string
	From     string // ex "contact@example.org"
	AdminTo  string
	Logger   *slog.Logger
}

// OutboxMsg représente un message en attente dans email_outbox.
type OutboxMsg struct {
	ID         string
	ToAddr     string
	Subject    string
	BodyText   string
	BodyHTML   string
	Attempts   int
	RetryAfter sql.NullString
}

// Enqueue insère un email en status=pending dans email_outbox.
func (m *Mailer) Enqueue(ctx context.Context, to, subject, bodyText, bodyHTML string) error {
	id := uuid.New().String()
	_, err := m.DB.ExecContext(ctx,
		`INSERT INTO email_outbox(id, to_addr, subject, body_text, body_html, status, attempts)
		 VALUES(?,?,?,?,?,'pending',0)`,
		id, to, subject, bodyText, bodyHTML,
	)
	if err != nil {
		return fmt.Errorf("mailer.Enqueue: %w", err)
	}
	return nil
}

// SendNow tente d'envoyer immédiatement. Backend SMTPS si SMTPHost défini,
// sinon Resend si APIKey défini. Si rien, no-op (reste pending). Backoff expo
// appliqué sur erreur transitoire.
func (m *Mailer) SendNow(ctx context.Context, msg OutboxMsg) error {
	if m.SMTPHost != "" {
		return m.sendSMTP(ctx, msg)
	}
	if m.APIKey == "" {
		m.log().Warn("mailer désactivé (ni SMTP ni APIKey), email reste en pending", "id", msg.ID)
		return nil
	}

	payload := map[string]any{
		"from":    m.From,
		"to":      []string{msg.ToAddr},
		"subject": msg.Subject,
		"text":    msg.BodyText,
		"html":    msg.BodyHTML,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mailer.SendNow build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil || (resp != nil && resp.StatusCode >= 400) {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			errMsg = fmt.Sprintf("Resend HTTP %d", resp.StatusCode)
			resp.Body.Close()
		}
		m.applyBackoff(ctx, msg, errMsg)
		return fmt.Errorf("mailer.SendNow: %s", errMsg)
	}
	resp.Body.Close()

	_, dbErr := m.DB.ExecContext(ctx,
		`UPDATE email_outbox SET status='sent', sent_at=CURRENT_TIMESTAMP WHERE id=?`,
		msg.ID,
	)
	return dbErr
}

// ApplyBackoff applique le backoff exponentiel 30/60/120/240/480s sur un envoi échoué.
// Exporté pour permettre les tests unitaires de la logique de retry.
func (m *Mailer) ApplyBackoff(ctx context.Context, msg OutboxMsg, errMsg string) {
	m.applyBackoff(ctx, msg, errMsg)
}

// applyBackoff : backoff exponentiel 30/60/120/240/480s, status=failed après 5 retries.
func (m *Mailer) applyBackoff(ctx context.Context, msg OutboxMsg, errMsg string) {
	delays := []int{30, 60, 120, 240, 480}
	delay := delays[len(delays)-1]
	if msg.Attempts < len(delays) {
		delay = delays[msg.Attempts]
	}
	nextRetry := time.Now().Add(time.Duration(delay) * time.Second).UTC().Format("2006-01-02 15:04:05")

	if msg.Attempts >= 4 {
		// Backoff best-effort : une erreur DB ici ne doit pas masquer l'erreur d'envoi originale.
		m.DB.ExecContext(ctx, //nolint:errcheck
			`UPDATE email_outbox SET status='failed', attempts=attempts+1, last_error=?, retry_after=? WHERE id=?`,
			errMsg, nextRetry, msg.ID,
		)
		m.log().Error("mailer: email définitivement échoué", "id", msg.ID, "to", msg.ToAddr)
	} else {
		// Backoff best-effort : une erreur DB ici ne doit pas masquer l'erreur d'envoi originale.
		m.DB.ExecContext(ctx, //nolint:errcheck
			`UPDATE email_outbox SET attempts=attempts+1, last_error=?, retry_after=? WHERE id=?`,
			errMsg, nextRetry, msg.ID,
		)
	}
}

// RunWorker démarre la boucle de drain de l'outbox. Ticker 30s.
func (m *Mailer) RunWorker(ctx context.Context) {
	m.log().Info("mailer worker démarré")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Premier drain immédiat
	m.drainOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.drainOnce(ctx)
		}
	}
}

func (m *Mailer) drainOnce(ctx context.Context) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT id, to_addr, subject, body_text, body_html, attempts, retry_after
		FROM email_outbox
		WHERE status='pending'
		AND (retry_after IS NULL OR retry_after <= CURRENT_TIMESTAMP)
		ORDER BY created_at ASC
		LIMIT 20
	`)
	if err != nil {
		m.log().Error("mailer drain query", "err", err)
		return
	}
	defer rows.Close()

	var msgs []OutboxMsg
	for rows.Next() {
		var msg OutboxMsg
		if err := rows.Scan(&msg.ID, &msg.ToAddr, &msg.Subject, &msg.BodyText, &msg.BodyHTML, &msg.Attempts, &msg.RetryAfter); err != nil {
			m.log().Error("mailer drain scan", "err", err)
			continue
		}
		msgs = append(msgs, msg)
	}
	rows.Close()

	for _, msg := range msgs {
		if err := m.SendNow(ctx, msg); err != nil {
			m.log().Warn("mailer send failed", "id", msg.ID, "err", err)
		}
	}
}

func (m *Mailer) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}
