// CLAUDE:SUMMARY Donations store — UPSERT idempotent helloasso_payment_id, mapping email→user_id, RGPD soft-erase (M-ASSOKIT-SPRINT3-S2).
// CLAUDE:WARN Pas de création automatique de user. UPSERT mappe payment_status updates en place.
package helloasso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Donation représente une row donations.
type Donation struct {
	ID                 string
	HelloAssoPaymentID string
	FormSlug           string
	FormType           string
	AmountCents        int64
	Currency           string
	UserID             sql.NullString
	DonorEmail         string
	DonorName          string
	PaymentStatus      string
	PaidAt             sql.NullString
	RefundedAt         sql.NullString
	RawEventID         string
}

// DonationsStore wrap DB operations.
type DonationsStore struct {
	DB *sql.DB
}

// UpsertFromPayment insère ou met à jour une donation à partir d'un Payment HelloAsso.
// Idempotent : même helloasso_payment_id → UPDATE in-place (status/paid_at/refunded_at).
// Mapping email → user_id : si payer.email match users.email, user_id renseigné.
// Pas de création automatique de user (RGPD anonymous donor friendly).
func (s *DonationsStore) UpsertFromPayment(ctx context.Context, p Payment, formSlug, formType, rawEventID string) error {
	if p.ID == 0 {
		return errors.New("donations.Upsert: payment.ID=0")
	}
	if rawEventID == "" {
		return errors.New("donations.Upsert: raw_event_id requis")
	}

	// Mapping email → user_id (lookup non-bloquant : si fail SELECT, on stocke NULL).
	var userID sql.NullString
	if p.PayerEmail != "" {
		var uid string
		err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM users WHERE LOWER(email) = LOWER(?) LIMIT 1`, p.PayerEmail,
		).Scan(&uid)
		if err == nil && uid != "" {
			userID = sql.NullString{String: uid, Valid: true}
		}
	}

	status := mapPaymentState(p.State)
	amountCents := int64(p.Amount * 100)
	helloAssoID := fmt.Sprintf("%d", p.ID)

	// PaidAt si status=paid ; RefundedAt si status=refunded.
	var paidAt, refundedAt sql.NullString
	if status == "paid" {
		paidAt = sql.NullString{String: parseDateOrNow(p.Date), Valid: true}
	}
	if status == "refunded" {
		refundedAt = sql.NullString{String: parseDateOrNow(p.Date), Valid: true}
	}

	id := uuid.New().String()
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO donations(id, helloasso_payment_id, helloasso_form_slug, helloasso_form_type,
			amount_cents, currency, user_id, donor_email, donor_name, payment_status,
			paid_at, refunded_at, raw_event_id, updated_at)
		VALUES (?, ?, ?, ?, ?, 'EUR', ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(helloasso_payment_id) DO UPDATE SET
			payment_status = excluded.payment_status,
			paid_at = COALESCE(excluded.paid_at, donations.paid_at),
			refunded_at = COALESCE(excluded.refunded_at, donations.refunded_at),
			user_id = COALESCE(donations.user_id, excluded.user_id),
			updated_at = CURRENT_TIMESTAMP
	`, id, helloAssoID, formSlug, formType, amountCents,
		userID, p.PayerEmail, p.PayerName, status,
		paidAt, refundedAt, rawEventID)
	if err != nil {
		return fmt.Errorf("donations.Upsert: %w", err)
	}
	return nil
}

// SoftEraseDonor RGPD : efface email + name d'une donation (row gardée).
// Réversible non — donor_email='', donor_name='[supprimé RGPD]'.
func (s *DonationsStore) SoftEraseDonor(ctx context.Context, donationID string) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE donations
		SET donor_email = '', donor_name = '[supprimé RGPD]', updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, donationID)
	if err != nil {
		return fmt.Errorf("donations.SoftErase: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("donations.SoftErase: donation introuvable")
	}
	return nil
}

// GetByHelloAssoPaymentID retourne une donation par son ID HelloAsso (test/admin).
func (s *DonationsStore) GetByHelloAssoPaymentID(ctx context.Context, paymentID string) (*Donation, error) {
	d := &Donation{}
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, helloasso_payment_id, helloasso_form_slug, helloasso_form_type,
			amount_cents, currency, user_id, donor_email, donor_name, payment_status,
			paid_at, refunded_at, raw_event_id
		FROM donations WHERE helloasso_payment_id = ?
	`, paymentID).Scan(
		&d.ID, &d.HelloAssoPaymentID, &d.FormSlug, &d.FormType,
		&d.AmountCents, &d.Currency, &d.UserID, &d.DonorEmail, &d.DonorName, &d.PaymentStatus,
		&d.PaidAt, &d.RefundedAt, &d.RawEventID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// mapPaymentState convertit le State HelloAsso vers le payment_status interne.
func mapPaymentState(state string) string {
	switch state {
	case "Pending":
		return "pending"
	case "Authorized":
		return "authorized"
	case "Authenticated", "Captured", "Paid":
		return "paid"
	case "Refunded":
		return "refunded"
	case "Refused", "Cancelled", "ContractError", "Expired":
		return "failed"
	default:
		return "pending"
	}
}

// parseDateOrNow tente de parser un timestamp HelloAsso, fallback sur now UTC.
func parseDateOrNow(s string) string {
	if s == "" {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return s
}
