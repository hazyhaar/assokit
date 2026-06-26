// CLAUDE:SUMMARY Modèle de revenu marketplace : commission config-driven (bps) + abonnement dealer via SubscriptionStore. Aucun hardcode de taux. Import-clean (publiable).
package billing

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// ErrUnknownPaymentMode est retourné quand payment_mode n'est pas une valeur connue.
var ErrUnknownPaymentMode = errors.New("billing: payment_mode inconnu (valeurs : commission, subscription)")

// PaymentMode représente le mode de revenu configuré pour un silo.
type PaymentMode string

const (
	// ModeCommission : revenus par pourcentage sur chaque transaction.
	ModeCommission PaymentMode = "commission"
	// ModeSubscription : revenus par abonnement dealer récurrent.
	ModeSubscription PaymentMode = "subscription"
)

// RevenueConfig contient le modèle de revenu tel que défini dans le toml silo.
type RevenueConfig struct {
	// PaymentMode détermine la règle applicable : "commission" ou "subscription".
	PaymentMode PaymentMode `toml:"payment_mode"`
	// SubscriptionCents est le montant en centimes de la cotisation dealer par période.
	// Ignoré si PaymentMode != ModeSubscription.
	SubscriptionCents int64 `toml:"subscription_cents"`
	// CommissionBPS est le taux en points de base (1 bps = 0,01 %).
	// Ignoré si PaymentMode != ModeCommission.
	CommissionBPS int `toml:"commission_bps"`
}

// siloFile est la struct intermédiaire pour parser uniquement la section [billing] du toml silo.
type siloFile struct {
	Billing RevenueConfig `toml:"billing"`
}

// LoadRevenue lit la section [billing] du fichier toml de silo indiqué par siloTomlPath.
// Retourne une erreur si payment_mode est absent ou non reconnu.
func LoadRevenue(siloTomlPath string) (RevenueConfig, error) {
	data, err := os.ReadFile(siloTomlPath)
	if err != nil {
		return RevenueConfig{}, fmt.Errorf("billing.LoadRevenue: lecture %s: %w", siloTomlPath, err)
	}
	var f siloFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return RevenueConfig{}, fmt.Errorf("billing.LoadRevenue: parse toml: %w", err)
	}
	cfg := f.Billing
	if cfg.PaymentMode != ModeCommission && cfg.PaymentMode != ModeSubscription {
		return RevenueConfig{}, fmt.Errorf("%w: %q", ErrUnknownPaymentMode, cfg.PaymentMode)
	}
	return cfg, nil
}

// ComputeCommission calcule la commission en centimes pour un montant de transaction
// donné en centimes, selon un taux exprimé en points de base (bps).
//
// Règle d'arrondi : arrondi au centime inférieur (troncature entière).
// Fondement : la plateforme ne prélève jamais plus que le calcul exact (favorable au vendeur),
// cohérent avec les pratiques de PSP. Exemple : 25 000 000 cts × 250 bps / 10 000 = 625 000 cts.
//
// Préconditions : amountCents >= 0, bps >= 0. Valeurs négatives retournent 0.
func ComputeCommission(amountCents int64, bps int) int64 {
	if amountCents <= 0 || bps <= 0 {
		return 0
	}
	// Multiplication en int64 avant division pour éviter toute perte de précision.
	return amountCents * int64(bps) / 10000
}

// SubscriptionStore est l'interface définie côté billing pour créer et interroger
// les abonnements dealer. membership.Store satisfait cette interface.
type SubscriptionStore interface {
	// Create enregistre une nouvelle adhésion. Retourne l'ID créé.
	Create(ctx context.Context, m SubscriptionRecord) (string, error)
	// CurrentStatus retourne le statut de l'adhésion active courante ("active" ou "none").
	CurrentStatus(ctx context.Context, userID string) (string, error)
}

// SubscriptionRecord représente les données nécessaires à la création d'un abonnement dealer.
// Les champs correspondent aux champs utilisés par membership.Membership.
type SubscriptionRecord struct {
	UserID      string
	PeriodStart string
	PeriodEnd   string
	AmountCents int64
	Status      string
	Note        string
}

// EnsureDealerSubscription crée un abonnement dealer pour userID si aucun abonnement
// actif n'existe déjà. Utilise cfg.SubscriptionCents comme montant.
// Idempotent : retourne ("", nil) si un abonnement actif existe déjà.
// periodStart et periodEnd sont des dates ISO 8601 (YYYY-MM-DD).
func EnsureDealerSubscription(ctx context.Context, store SubscriptionStore, cfg RevenueConfig, userID, periodStart, periodEnd string) (id string, err error) {
	if cfg.PaymentMode != ModeSubscription {
		return "", fmt.Errorf("billing.EnsureDealerSubscription: silo configuré en %q, pas en subscription", cfg.PaymentMode)
	}
	current, err := store.CurrentStatus(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("billing.EnsureDealerSubscription: CurrentStatus: %w", err)
	}
	if current == "active" {
		// Abonnement actif déjà présent — idempotent.
		return "", nil
	}
	id, err = store.Create(ctx, SubscriptionRecord{
		UserID:      userID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		AmountCents: cfg.SubscriptionCents,
		Status:      "pending",
		Note:        "abonnement dealer marketplace",
	})
	if err != nil {
		return "", fmt.Errorf("billing.EnsureDealerSubscription: Create: %w", err)
	}
	return id, nil
}
