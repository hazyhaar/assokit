// CLAUDE:SUMMARY Pattern Connector — interface stable pour services tiers (HelloAsso, Stripe, etc.) (M-ASSOKIT-SPRINT2-S1).
// CLAUDE:WARN ConfigSchema valide les params au moment de Configure ; Start ne re-valide pas. Health.OK=false signale un état dégradé non-bloquant.
package connectors

import (
	"context"
	"errors"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Health représente l'état runtime d'un connecteur.
type Health struct {
	OK        bool          // true si Ping a réussi
	Message   string        // "OK", "Token expired", etc.
	Latency   time.Duration // durée du dernier Ping
	LastCheck time.Time
}

// Connector est l'interface qu'un service tiers doit implémenter pour
// être enregistré dans le Registry.
type Connector interface {
	// ID retourne l'identifiant stable du connecteur (ex "helloasso", "stripe").
	ID() string
	// DisplayName retourne le nom affiché dans l'admin UI.
	DisplayName() string
	// Description : phrase courte FR pour LLM + admin.
	Description() string
	// ConfigSchema retourne le schéma JSON des params requis pour Configure.
	ConfigSchema() *jsonschema.Schema
	// Start initialise la connexion (OAuth handshake, token refresh, etc.).
	// Appelé au boot pour chaque connecteur enabled=1.
	Start(ctx context.Context, cfg map[string]any) error
	// Stop libère les ressources (TCP conn, goroutines, etc.).
	Stop(ctx context.Context) error
	// Ping vérifie l'état (token valide ? endpoint reachable ?).
	// Doit être idempotent et rapide (<5s).
	Ping(ctx context.Context) (Health, error)
}

// Erreurs sentinelles exportées.
var (
	ErrDuplicateConnector = errors.New("connectors: connector déjà enregistré avec cet ID")
	ErrNotFound           = errors.New("connectors: connector non trouvé")
)
