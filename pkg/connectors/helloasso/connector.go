// CLAUDE:SUMMARY HelloAsso Connector — implémente Connector interface (Start/Stop/Ping/HandleWebhook stub) (M-ASSOKIT-SPRINT3-S1).
// CLAUDE:WARN HandleWebhook stub : implémentation S3-2. Ping vérifie /v5/users/me OK = healthy.
package helloasso

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
)

// Config : params non-sensibles persistés dans connectors.config_json.
type Config struct {
	OrganizationSlug string `json:"organization_slug"`
	Sandbox          bool   `json:"sandbox"`
	ClientID         string `json:"client_id"`
}

// Connector implémente connectors.Connector pour HelloAsso.
type Connector struct {
	Vault *assets.Vault

	cfg     Config
	httpCli *http.Client
	api     *APIClient
}

// New construit un Connector vide. Start renseigne les params depuis cfg.
func New(vault *assets.Vault) *Connector {
	return &Connector{Vault: vault}
}

func (c *Connector) ID() string          { return "helloasso" }
func (c *Connector) DisplayName() string { return "HelloAsso" }
func (c *Connector) Description() string {
	return "Connecteur HelloAsso — paiements (don, adhésion), formulaires, webhooks. OAuth2 client_credentials."
}

const configSchemaJSON = `{
	"type":"object",
	"properties":{
		"organization_slug":{"type":"string","minLength":1,"description":"Slug HelloAsso (ex: nonpossumus)"},
		"sandbox":{"type":"boolean","description":"true=api.helloasso-sandbox.com, false=api.helloasso.com"},
		"client_id":{"type":"string","minLength":1,"description":"Client ID OAuth2 (back-office HelloAsso)"},
		"client_secret":{"type":"string","format":"password","description":"Secret OAuth2 (chiffré via Vault)"}
	},
	"required":["organization_slug","client_id","client_secret"]
}`

// schemaCompiled : compilé une seule fois, partagé par les instances.
var schemaCompiled *jsonschema.Schema

func init() {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://nonpossumus/connectors/helloasso", strings.NewReader(configSchemaJSON)); err == nil {
		s, err := c.Compile("https://nonpossumus/connectors/helloasso")
		if err == nil {
			schemaCompiled = s
		}
	}
}

func (c *Connector) ConfigSchema() *jsonschema.Schema { return schemaCompiled }

// Start charge la config + initialise le client OAuth2 + APIClient.
func (c *Connector) Start(ctx context.Context, cfg map[string]any) error {
	if v, ok := cfg["organization_slug"].(string); ok {
		c.cfg.OrganizationSlug = v
	}
	if v, ok := cfg["sandbox"].(bool); ok {
		c.cfg.Sandbox = v
	}
	if v, ok := cfg["client_id"].(string); ok {
		c.cfg.ClientID = v
	}
	if c.cfg.OrganizationSlug == "" || c.cfg.ClientID == "" {
		return fmt.Errorf("helloasso.Start: organization_slug et client_id requis")
	}
	if c.Vault == nil {
		return fmt.Errorf("helloasso.Start: Vault requis pour client_secret")
	}

	httpCli, _, err := NewOAuthHTTPClient(ctx, c.Vault, c.ID(), c.cfg.ClientID, TokenURLFor(c.cfg.Sandbox))
	if err != nil {
		return fmt.Errorf("helloasso.Start oauth: %w", err)
	}
	c.httpCli = httpCli
	c.api = &APIClient{HTTP: httpCli, BaseURL: APIBaseFor(c.cfg.Sandbox)}
	return nil
}

// Stop libère les ressources (le httpCli n'a pas de Close explicite, juste GC).
func (c *Connector) Stop(ctx context.Context) error {
	c.httpCli = nil
	c.api = nil
	return nil
}

// Ping vérifie que le token OAuth2 est valide via GET /v5/users/me.
func (c *Connector) Ping(ctx context.Context) (connectors.Health, error) {
	start := time.Now()
	if c.httpCli == nil {
		return connectors.Health{OK: false, Message: "non démarré (Start non appelé)"}, errors.New("not started")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(pingCtx, http.MethodGet, APIBaseFor(c.cfg.Sandbox)+"/v5/users/me", nil)
	resp, err := c.httpCli.Do(req)
	latency := time.Since(start)
	if err != nil {
		return connectors.Health{OK: false, Message: "erreur réseau: " + err.Error(), Latency: latency}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return connectors.Health{OK: false, Message: "token invalide ou expiré (401)", Latency: latency}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return connectors.Health{OK: false, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), Latency: latency}, nil
	}
	return connectors.Health{OK: true, Message: "OK", Latency: latency}, nil
}

// HandleWebhook : stub pour S3-2. Reconnaît le event_type connu, log un info,
// et fail-soft si payload invalide. L'implémentation complète (création paiement
// en DB, dispatch vers handlers métier) est livrée en S3-2.
func (c *Connector) HandleWebhook(ctx context.Context, eventType string, payload []byte) error {
	if !bytes.HasPrefix(bytes.TrimSpace(payload), []byte("{")) {
		return fmt.Errorf("helloasso.HandleWebhook: payload non-JSON")
	}
	switch eventType {
	case "Payment", "payment.completed", "payment.refunded", "Order":
		// stub : noop, S3-2 implémentera l'INSERT donations / orders.
		return nil
	default:
		// event inconnu = no-op (forward-compat, ne pas planter sur nouveaux types).
		return nil
	}
}

// API expose l'APIClient configuré (pour callers internes : workers, jobs).
func (c *Connector) API() *APIClient { return c.api }

// Cfg expose la config (utilisé par tests + reporting admin).
func (c *Connector) Cfg() Config { return c.cfg }
