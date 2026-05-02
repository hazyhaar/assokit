// CLAUDE:SUMMARY Tests donations — UPSERT idempotent, mapping email→user_id, RGPD soft-erase (M-ASSOKIT-SPRINT3-S2).
package helloasso

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openDonationsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE);
		CREATE TABLE webhook_events (id TEXT PRIMARY KEY);
		CREATE TABLE donations (
			id TEXT PRIMARY KEY,
			helloasso_payment_id TEXT NOT NULL UNIQUE,
			helloasso_form_slug TEXT NOT NULL DEFAULT '',
			helloasso_form_type TEXT NOT NULL DEFAULT '',
			amount_cents INTEGER NOT NULL,
			currency TEXT NOT NULL DEFAULT 'EUR',
			user_id TEXT,
			donor_email TEXT NOT NULL DEFAULT '',
			donor_name TEXT NOT NULL DEFAULT '',
			payment_status TEXT NOT NULL,
			paid_at TEXT, refunded_at TEXT,
			raw_event_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestDonations_UpsertCreatesRow : Upsert sur ID nouveau → 1 row pending/paid.
func TestDonations_UpsertCreatesRow(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	p := Payment{ID: 100, Amount: 25.5, State: "Authorized", PayerEmail: "alice@example.org", PayerName: "Alice"}
	if err := store.UpsertFromPayment(context.Background(), p, "don", "Donation", "evt-1"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	d, err := store.GetByHelloAssoPaymentID(context.Background(), "100")
	if err != nil || d == nil {
		t.Fatalf("Get: %v", err)
	}
	if d.AmountCents != 2550 {
		t.Errorf("amount_cents = %d, attendu 2550", d.AmountCents)
	}
	if d.PaymentStatus != "authorized" {
		t.Errorf("status = %q, attendu authorized", d.PaymentStatus)
	}
	if d.DonorEmail != "alice@example.org" {
		t.Errorf("donor_email = %q", d.DonorEmail)
	}
}

// TestDonations_UpsertIdempotentOnSamePaymentID : 2× même ID → 1 row.
func TestDonations_UpsertIdempotentOnSamePaymentID(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	p := Payment{ID: 200, Amount: 10, State: "Authorized"}
	store.UpsertFromPayment(context.Background(), p, "f", "t", "evt-2") //nolint:errcheck
	store.UpsertFromPayment(context.Background(), p, "f", "t", "evt-2") //nolint:errcheck
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM donations WHERE helloasso_payment_id='200'`).Scan(&n)
	if n != 1 {
		t.Errorf("count = %d, attendu 1", n)
	}
}

// TestDonations_PaymentStatusUpdateReflected : authorized puis paid → status updaté in place.
func TestDonations_PaymentStatusUpdateReflected(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	p := Payment{ID: 300, Amount: 50, State: "Authorized", Date: "2026-05-02T10:00:00Z"}
	store.UpsertFromPayment(context.Background(), p, "", "", "e-1") //nolint:errcheck

	p.State = "Paid"
	p.Date = "2026-05-02T10:05:00Z"
	store.UpsertFromPayment(context.Background(), p, "", "", "e-2") //nolint:errcheck

	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "300")
	if d.PaymentStatus != "paid" {
		t.Errorf("post-update status = %q, attendu paid", d.PaymentStatus)
	}
	if !d.PaidAt.Valid {
		t.Error("paid_at non posé")
	}
}

// TestDonations_RefundUpdatesStatusAndRefundedAt : Refunded → status+refunded_at.
func TestDonations_RefundUpdatesStatusAndRefundedAt(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	store.UpsertFromPayment(context.Background(), Payment{ID: 400, Amount: 10, State: "Paid", Date: "2026-05-01T10:00:00Z"}, "", "", "e-1") //nolint:errcheck
	store.UpsertFromPayment(context.Background(), Payment{ID: 400, Amount: 10, State: "Refunded", Date: "2026-05-02T10:00:00Z"}, "", "", "e-2") //nolint:errcheck

	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "400")
	if d.PaymentStatus != "refunded" {
		t.Errorf("status = %q, attendu refunded", d.PaymentStatus)
	}
	if !d.RefundedAt.Valid {
		t.Error("refunded_at non posé")
	}
}

// TestDonations_MapsToExistingUserByEmail : payer email match users → user_id non null.
func TestDonations_MapsToExistingUserByEmail(t *testing.T) {
	db := openDonationsDB(t)
	db.Exec(`INSERT INTO users(id, email) VALUES('u-1', 'bob@example.org')`) //nolint:errcheck

	store := &DonationsStore{DB: db}
	p := Payment{ID: 500, Amount: 20, State: "Paid", PayerEmail: "bob@example.org"}
	store.UpsertFromPayment(context.Background(), p, "", "", "e") //nolint:errcheck

	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "500")
	if !d.UserID.Valid || d.UserID.String != "u-1" {
		t.Errorf("user_id = %+v, attendu u-1", d.UserID)
	}
}

// TestDonations_AnonymousDonationStoresEmailForFuture : email pas match → user_id NULL + donor_email gardé.
func TestDonations_AnonymousDonationStoresEmailForFuture(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	p := Payment{ID: 600, Amount: 10, State: "Paid", PayerEmail: "anon@example.org"}
	store.UpsertFromPayment(context.Background(), p, "", "", "e") //nolint:errcheck

	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "600")
	if d.UserID.Valid {
		t.Errorf("user_id valid alors qu'aucun match : %+v", d.UserID)
	}
	if d.DonorEmail != "anon@example.org" {
		t.Errorf("donor_email = %q, attendu gardé pour mapping futur", d.DonorEmail)
	}
}

// TestDonations_GDPRSoftDeleteEmailKeepsRow : SoftErase → email vide, row persiste.
func TestDonations_GDPRSoftDeleteEmailKeepsRow(t *testing.T) {
	db := openDonationsDB(t)
	store := &DonationsStore{DB: db}
	store.UpsertFromPayment(context.Background(), Payment{ID: 700, Amount: 30, State: "Paid", PayerEmail: "victim@example.org", PayerName: "Victim Smith"}, "", "", "e") //nolint:errcheck
	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "700")

	if err := store.SoftEraseDonor(context.Background(), d.ID); err != nil {
		t.Fatalf("SoftErase: %v", err)
	}

	d2, _ := store.GetByHelloAssoPaymentID(context.Background(), "700")
	if d2 == nil {
		t.Fatal("row supprimée, attendu gardée")
	}
	if d2.DonorEmail != "" {
		t.Errorf("donor_email = %q, attendu vide", d2.DonorEmail)
	}
	if d2.DonorName != "[supprimé RGPD]" {
		t.Errorf("donor_name = %q, attendu placeholder", d2.DonorName)
	}
}

// TestDonations_MapPaymentState : helpers de mapping.
func TestDonations_MapPaymentState(t *testing.T) {
	cases := map[string]string{
		"Authorized": "authorized", "Paid": "paid", "Captured": "paid",
		"Refunded": "refunded", "Refused": "failed", "Pending": "pending",
		"Inconnu": "pending",
	}
	for in, want := range cases {
		if got := mapPaymentState(in); got != want {
			t.Errorf("mapPaymentState(%q) = %q, want %q", in, got, want)
		}
	}
}
