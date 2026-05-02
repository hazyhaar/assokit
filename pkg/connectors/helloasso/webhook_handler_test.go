// CLAUDE:SUMMARY Tests webhook_handler — HMAC verify, dispatch event types, donations created (M-ASSOKIT-SPRINT3-S2).
package helloasso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestWebhook_HMACVerifySuccess : signature correcte → true.
func TestWebhook_HMACVerifySuccess(t *testing.T) {
	payload := []byte(`{"eventType":"Payment","data":{"id":42}}`)
	secret := "shared-secret-123"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))

	if !VerifyWebhookSignature(payload, sig, secret) {
		t.Error("signature correcte refusée")
	}
}

// TestWebhook_HMACVerifyMismatch : signature wrong → false.
func TestWebhook_HMACVerifyMismatch(t *testing.T) {
	payload := []byte(`{"eventType":"x"}`)
	if VerifyWebhookSignature(payload, "WRONG_SIG", "secret") {
		t.Error("signature wrong acceptée (timing attack possible si == au lieu de hmac.Equal)")
	}
}

// TestWebhook_HMACVerifyEmptySecretReturnsFalse : secret vide → toujours false.
func TestWebhook_HMACVerifyEmptySecretReturnsFalse(t *testing.T) {
	if VerifyWebhookSignature([]byte("x"), "any", "") {
		t.Error("secret vide accepté (bypass possible)")
	}
}

// TestWebhook_VerifyFromVault : Vault stocke secret, verify OK.
func TestWebhook_VerifyFromVault(t *testing.T) {
	_, vault := openOAuthDB(t)
	if err := vault.Set(context.Background(), "helloasso", "webhook_signing_secret", "vault-secret", "test"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	payload := []byte(`{"id":1}`)
	mac := hmac.New(sha256.New, []byte("vault-secret"))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))

	ok, err := VerifyWebhookFromVault(context.Background(), vault, payload, sig)
	if err != nil || !ok {
		t.Errorf("VerifyWebhookFromVault ok=%v err=%v, attendu true+nil", ok, err)
	}
}

// setupProcessTest construit DB + tables nécessaires pour ProcessWebhook.
func setupProcessTest(t *testing.T) *Connector {
	t.Helper()
	db := openDonationsDB(t)
	t.Cleanup(func() { db.Close() })
	c := New(nil)
	// Le test attache directement le DB via store, le connector lui-même n'a pas besoin de Start.
	return c
}

// TestWebhook_ProcessOrderEventCreatesDonations : Order avec 1 payment → 1 donation.
func TestWebhook_ProcessOrderEventCreatesDonations(t *testing.T) {
	db := openDonationsDB(t)
	c := New(nil)
	payload := []byte(`{
		"eventType":"Order",
		"data":{
			"id":1001,
			"formSlug":"don-2026",
			"formType":"Donation",
			"payments":[
				{"id":2001,"amount":15.0,"state":"Authorized","payerEmail":"a@b.com","payerName":"A B"}
			]
		}
	}`)
	if err := c.ProcessWebhook(context.Background(), db, "Order", payload, "raw-1"); err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	store := &DonationsStore{DB: db}
	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "2001")
	if d == nil {
		t.Fatal("donation non créée")
	}
	if d.AmountCents != 1500 {
		t.Errorf("amount = %d, attendu 1500", d.AmountCents)
	}
	if d.FormSlug != "don-2026" {
		t.Errorf("form_slug = %q", d.FormSlug)
	}
}

// TestWebhook_ProcessPaymentEventCreatesOrUpdates : Payment.Notification flat top-level data.
func TestWebhook_ProcessPaymentEventCreatesOrUpdates(t *testing.T) {
	db := openDonationsDB(t)
	c := New(nil)
	payload := []byte(`{
		"eventType":"Payment",
		"data":{"id":3000,"amount":50,"state":"Paid","date":"2026-05-02T10:00:00Z","payerEmail":"x@y.com","formSlug":"f","formType":"Donation"}
	}`)
	if err := c.ProcessWebhook(context.Background(), db, "Payment", payload, "raw-2"); err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	store := &DonationsStore{DB: db}
	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "3000")
	if d == nil || d.PaymentStatus != "paid" {
		t.Errorf("d=%+v, attendu paid", d)
	}
}

// TestWebhook_ProcessRefundEventForcesRefundedStatus : Refunded event → status='refunded'.
func TestWebhook_ProcessRefundEventForcesRefundedStatus(t *testing.T) {
	db := openDonationsDB(t)
	c := New(nil)
	// 1ère injection : Paid
	c.ProcessWebhook(context.Background(), db, "Payment", []byte(`{"data":{"id":4000,"amount":10,"state":"Paid","date":"2026-05-01T00:00:00Z"}}`), "r-1") //nolint:errcheck
	// 2e injection via Payment.Refunded (state ignoré, force refunded)
	if err := c.ProcessWebhook(context.Background(), db, "Payment.Refunded", []byte(`{"data":{"id":4000,"amount":10,"state":"Authorized","date":"2026-05-02T00:00:00Z"}}`), "r-2"); err != nil {
		t.Fatalf("ProcessWebhook refund: %v", err)
	}
	store := &DonationsStore{DB: db}
	d, _ := store.GetByHelloAssoPaymentID(context.Background(), "4000")
	if d.PaymentStatus != "refunded" {
		t.Errorf("status après refund = %q, attendu refunded", d.PaymentStatus)
	}
	if !d.RefundedAt.Valid {
		t.Error("refunded_at non posé")
	}
}

// TestWebhook_ProcessUnknownEventTypeReturnsError : event_type non listé → error.
func TestWebhook_ProcessUnknownEventTypeReturnsError(t *testing.T) {
	db := openDonationsDB(t)
	c := New(nil)
	err := c.ProcessWebhook(context.Background(), db, "Unknown.Type", []byte(`{}`), "r")
	if err == nil {
		t.Error("attendu erreur sur event inconnu")
	}
	if !strings.Contains(err.Error(), "non géré") {
		t.Errorf("err = %v, attendu mention 'non géré'", err)
	}
}
