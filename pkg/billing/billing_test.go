package billing_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/assokit/pkg/billing"
)

// --- ComputeCommission ---

func TestComputeCommission_RealExample(t *testing.T) {
	// Vente yacht 250 000 € = 25 000 000 centimes, taux 250 bps (2,50 %).
	// Attendu : 25 000 000 × 250 / 10 000 = 625 000 centimes = 6 250 €.
	got := billing.ComputeCommission(25_000_000, 250)
	if got != 625_000 {
		t.Errorf("Commission 250 000€ @ 250 bps = 625 000 cts, obtenu %d", got)
	}
}

func TestComputeCommission_Truncation(t *testing.T) {
	// Cas non-entier : 1 centime à 1 bps = 0,0001 centime → arrondi inférieur = 0.
	got := billing.ComputeCommission(1, 1)
	if got != 0 {
		t.Errorf("arrondi inférieur attendu 0, obtenu %d", got)
	}
	// 99 centimes à 150 bps : 99 × 150 / 10000 = 14850/10000 = 1 (troncature, pas 2).
	got = billing.ComputeCommission(99, 150)
	if got != 1 {
		t.Errorf("arrondi inférieur attendu 1, obtenu %d", got)
	}
}

func TestComputeCommission_ZeroInputs(t *testing.T) {
	if billing.ComputeCommission(0, 250) != 0 {
		t.Error("montant 0 doit retourner 0")
	}
	if billing.ComputeCommission(100, 0) != 0 {
		t.Error("bps 0 doit retourner 0")
	}
	if billing.ComputeCommission(-1, 250) != 0 {
		t.Error("montant négatif doit retourner 0")
	}
}

func TestComputeCommission_SmallSale(t *testing.T) {
	// Vente 100 € = 10 000 centimes à 250 bps = 250 centimes = 2,50 €.
	got := billing.ComputeCommission(10_000, 250)
	if got != 250 {
		t.Errorf("100€ @ 250 bps = 250 cts, obtenu %d", got)
	}
}

// --- LoadRevenue ---

func writeTempToml(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "silo.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRevenue_Commission(t *testing.T) {
	path := writeTempToml(t, `
[billing]
payment_mode       = "commission"
commission_bps     = 250
subscription_cents = 0
`)
	cfg, err := billing.LoadRevenue(path)
	if err != nil {
		t.Fatalf("inattendu: %v", err)
	}
	if cfg.PaymentMode != billing.ModeCommission {
		t.Errorf("mode attendu commission, obtenu %q", cfg.PaymentMode)
	}
	if cfg.CommissionBPS != 250 {
		t.Errorf("bps attendu 250, obtenu %d", cfg.CommissionBPS)
	}
}

func TestLoadRevenue_Subscription(t *testing.T) {
	path := writeTempToml(t, `
[billing]
payment_mode       = "subscription"
commission_bps     = 0
subscription_cents = 4900
`)
	cfg, err := billing.LoadRevenue(path)
	if err != nil {
		t.Fatalf("inattendu: %v", err)
	}
	if cfg.PaymentMode != billing.ModeSubscription {
		t.Errorf("mode attendu subscription, obtenu %q", cfg.PaymentMode)
	}
	if cfg.SubscriptionCents != 4900 {
		t.Errorf("subscription_cents attendu 4900, obtenu %d", cfg.SubscriptionCents)
	}
}

func TestLoadRevenue_InvalidMode(t *testing.T) {
	path := writeTempToml(t, `
[billing]
payment_mode   = "freemium"
commission_bps = 0
`)
	_, err := billing.LoadRevenue(path)
	if !errors.Is(err, billing.ErrUnknownPaymentMode) {
		t.Errorf("attendu ErrUnknownPaymentMode, obtenu %v", err)
	}
}

func TestLoadRevenue_MissingSection(t *testing.T) {
	// Toml sans section [billing] : payment_mode sera vide → erreur.
	path := writeTempToml(t, `id = "test"`)
	_, err := billing.LoadRevenue(path)
	if !errors.Is(err, billing.ErrUnknownPaymentMode) {
		t.Errorf("section manquante doit retourner ErrUnknownPaymentMode, obtenu %v", err)
	}
}

// --- EnsureDealerSubscription ---

// stubStore est un SubscriptionStore de test entièrement en mémoire.
type stubStore struct {
	current string // retourné par CurrentStatus
	created []billing.SubscriptionRecord
	failOn  string // "create" ou "status" pour simuler une erreur
}

func (s *stubStore) Create(_ context.Context, m billing.SubscriptionRecord) (string, error) {
	if s.failOn == "create" {
		return "", errors.New("stub: create error")
	}
	s.created = append(s.created, m)
	return "stub-id-001", nil
}

func (s *stubStore) CurrentStatus(_ context.Context, _ string) (string, error) {
	if s.failOn == "status" {
		return "", errors.New("stub: status error")
	}
	return s.current, nil
}

func TestEnsureDealerSubscription_CreatesWhenNone(t *testing.T) {
	store := &stubStore{current: "none"}
	cfg := billing.RevenueConfig{
		PaymentMode:       billing.ModeSubscription,
		SubscriptionCents: 4900,
	}
	id, err := billing.EnsureDealerSubscription(context.Background(), store, cfg, "user-1", "2026-06-01", "2026-12-31")
	if err != nil {
		t.Fatalf("inattendu: %v", err)
	}
	if id == "" {
		t.Error("id de membership attendu, obtenu vide")
	}
	if len(store.created) != 1 {
		t.Fatalf("Create appelé %d fois, attendu 1", len(store.created))
	}
	if store.created[0].AmountCents != 4900 {
		t.Errorf("amount_cents attendu 4900, obtenu %d", store.created[0].AmountCents)
	}
}

func TestEnsureDealerSubscription_IdempotentWhenActive(t *testing.T) {
	store := &stubStore{current: "active"}
	cfg := billing.RevenueConfig{
		PaymentMode:       billing.ModeSubscription,
		SubscriptionCents: 4900,
	}
	id, err := billing.EnsureDealerSubscription(context.Background(), store, cfg, "user-1", "2026-06-01", "2026-12-31")
	if err != nil {
		t.Fatalf("inattendu: %v", err)
	}
	if id != "" {
		t.Errorf("id doit être vide si abonnement déjà actif, obtenu %q", id)
	}
	if len(store.created) != 0 {
		t.Error("Create ne doit pas être appelé si abonnement actif")
	}
}

func TestEnsureDealerSubscription_RejectsCommissionMode(t *testing.T) {
	store := &stubStore{current: "none"}
	cfg := billing.RevenueConfig{
		PaymentMode:   billing.ModeCommission,
		CommissionBPS: 250,
	}
	_, err := billing.EnsureDealerSubscription(context.Background(), store, cfg, "user-1", "2026-06-01", "2026-12-31")
	if err == nil {
		t.Error("erreur attendue pour mode commission, obtenu nil")
	}
}

func TestEnsureDealerSubscription_PropagatesStatusError(t *testing.T) {
	store := &stubStore{failOn: "status"}
	cfg := billing.RevenueConfig{
		PaymentMode:       billing.ModeSubscription,
		SubscriptionCents: 4900,
	}
	_, err := billing.EnsureDealerSubscription(context.Background(), store, cfg, "user-1", "2026-06-01", "2026-12-31")
	if err == nil {
		t.Error("erreur CurrentStatus doit être propagée")
	}
}
