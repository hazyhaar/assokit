// CLAUDE:SUMMARY Tests gardiens AdminConnectors — ACL admin, list shows status (M-ASSOKIT-SPRINT2-S1).
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// configurableConnector inclut un schema avec un champ password et un booléen.
type configurableConnector struct {
	id, name, desc string
	schema         *jsonschema.Schema
}

func (c *configurableConnector) ID() string                                            { return c.id }
func (c *configurableConnector) DisplayName() string                                   { return c.name }
func (c *configurableConnector) Description() string                                   { return c.desc }
func (c *configurableConnector) ConfigSchema() *jsonschema.Schema                      { return c.schema }
func (c *configurableConnector) Start(ctx context.Context, cfg map[string]any) error  { return nil }
func (c *configurableConnector) Stop(ctx context.Context) error                       { return nil }
func (c *configurableConnector) Ping(ctx context.Context) (connectors.Health, error)  { return connectors.Health{OK: true}, nil }

func newConfigurableConnector(t *testing.T, id, schemaJSON string) *configurableConnector {
	t.Helper()
	c := jsonschema.NewCompiler()
	if err := c.AddResource("test://schema", strings.NewReader(schemaJSON)); err != nil {
		t.Fatalf("compile add resource: %v", err)
	}
	s, err := c.Compile("test://schema")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return &configurableConnector{id: id, name: id, desc: "test", schema: s}
}

type fakeConnector struct{ id, name, desc string }

func (f *fakeConnector) ID() string                                            { return f.id }
func (f *fakeConnector) DisplayName() string                                   { return f.name }
func (f *fakeConnector) Description() string                                   { return f.desc }
func (f *fakeConnector) ConfigSchema() *jsonschema.Schema                      { return nil }
func (f *fakeConnector) Start(ctx context.Context, cfg map[string]any) error  { return nil }
func (f *fakeConnector) Stop(ctx context.Context) error                       { return nil }
func (f *fakeConnector) Ping(ctx context.Context) (connectors.Health, error)  { return connectors.Health{OK: true, Message: "OK"}, nil }

func setupAdminConnectorsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connectors (
			id TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}',
			configured_at TEXT, configured_by TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestAdminConnectors_NonAdminReturns403 : utilisateur non-admin → 403.
func TestAdminConnectors_NonAdminReturns403(t *testing.T) {
	db := setupAdminConnectorsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	reg := connectors.NewRegistry()

	req := httptest.NewRequest("GET", "/admin/connectors", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "u", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	AdminConnectorsList(deps, reg, nil)(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin code=%d, attendu 403", w.Code)
	}
}

// TestAdminConnectors_AnonymousReturns403 : pas de user en ctx → 403.
func TestAdminConnectors_AnonymousReturns403(t *testing.T) {
	db := setupAdminConnectorsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	reg := connectors.NewRegistry()

	req := httptest.NewRequest("GET", "/admin/connectors", nil)
	w := httptest.NewRecorder()
	AdminConnectorsList(deps, reg, nil)(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("anonymous code=%d, attendu 403", w.Code)
	}
}

// configureTestSchema : schema avec client_id (string), client_secret (password→secret), sandbox_mode (boolean).
const configureTestSchema = `{
	"type":"object",
	"properties":{
		"client_id":{"type":"string","minLength":1},
		"client_secret":{"type":"string","format":"password"},
		"sandbox_mode":{"type":"boolean"}
	},
	"required":["client_id","client_secret"]
}`

func setupVaultTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connectors (id TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}', configured_at TEXT, configured_by TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE connector_credentials (connector_id TEXT NOT NULL, key_name TEXT NOT NULL,
			encrypted_value BLOB NOT NULL, set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			set_by TEXT, rotated_at TEXT, PRIMARY KEY(connector_id, key_name));
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestAdminConnectorsConfigure_PersistsConfigViaVaultForSecrets : POST avec secret → Vault.Set, pas dans config_json.
func TestAdminConnectorsConfigure_PersistsConfigViaVaultForSecrets(t *testing.T) {
	db := setupVaultTestDB(t)
	const validKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	vault, _ := assets.NewVault(db, validKey)

	reg := connectors.NewRegistry()
	c := newConfigurableConnector(t, "helloasso", configureTestSchema)
	_ = reg.Register(c)

	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	r := chi.NewRouter()
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, vault))

	body := `{"client_id":"id123","client_secret":"SUPER_SECRET","sandbox_mode":true}`
	req := httptest.NewRequest("POST", "/admin/connectors/helloasso/configure", strings.NewReader(body))
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "admin", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("configure code=%d body=%s, attendu 200", w.Code, w.Body.String())
	}

	// config_json NE DOIT PAS contenir client_secret
	var cfgJSON string
	db.QueryRow(`SELECT config_json FROM connectors WHERE id='helloasso'`).Scan(&cfgJSON)
	if strings.Contains(cfgJSON, "SUPER_SECRET") {
		t.Errorf("config_json a leaké le secret : %s", cfgJSON)
	}
	if strings.Contains(cfgJSON, "client_secret") {
		t.Errorf("config_json contient la clé client_secret (devrait être en Vault) : %s", cfgJSON)
	}
	if !strings.Contains(cfgJSON, "client_id") {
		t.Errorf("config_json devrait contenir client_id (non-secret) : %s", cfgJSON)
	}

	// Vault DOIT contenir client_secret avec ciphertext non-clair
	var enc []byte
	db.QueryRow(`SELECT encrypted_value FROM connector_credentials WHERE connector_id='helloasso' AND key_name='client_secret'`).Scan(&enc)
	if len(enc) == 0 {
		t.Fatal("Vault ne contient pas client_secret")
	}
	if strings.Contains(string(enc), "SUPER_SECRET") {
		t.Errorf("vault encrypted_value contient le plaintext")
	}
}

// TestAdminConnectorsConfigure_ValidationFailsServerSide : POST données invalides → 400.
func TestAdminConnectorsConfigure_ValidationFailsServerSide(t *testing.T) {
	db := setupVaultTestDB(t)
	const validKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	vault, _ := assets.NewVault(db, validKey)
	reg := connectors.NewRegistry()
	_ = reg.Register(newConfigurableConnector(t, "helloasso", configureTestSchema))

	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := chi.NewRouter()
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, vault))

	// client_secret manquant (required violé)
	body := `{"client_id":"id"}`
	req := httptest.NewRequest("POST", "/admin/connectors/helloasso/configure", strings.NewReader(body))
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "admin", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("validation invalide code=%d, attendu 400", w.Code)
	}
}

// TestAdminConnectorsConfigure_NonAdminReturns403 : non-admin → 403.
func TestAdminConnectorsConfigure_NonAdminReturns403(t *testing.T) {
	db := setupVaultTestDB(t)
	reg := connectors.NewRegistry()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := chi.NewRouter()
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, nil))

	req := httptest.NewRequest("POST", "/admin/connectors/x/configure", strings.NewReader(`{}`))
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "u", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin code=%d, attendu 403", w.Code)
	}
}

// TestAdminConnectorSchema_ReturnsJSONSchema : GET /schema → JSON valide.
func TestAdminConnectorSchema_ReturnsJSONSchema(t *testing.T) {
	db := setupVaultTestDB(t)
	reg := connectors.NewRegistry()
	_ = reg.Register(newConfigurableConnector(t, "helloasso", configureTestSchema))

	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := chi.NewRouter()
	r.Get("/admin/connectors/{id}/schema", AdminConnectorSchema(deps, reg))

	req := httptest.NewRequest("GET", "/admin/connectors/helloasso/schema", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "admin", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("schema code=%d", w.Code)
	}
	var parsed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Errorf("schema response not JSON: %v body=%s", err, w.Body.String())
	}
}

// TestAdminConnectorsConfigure_NoSecretsInConfigJsonAfterPOST : régression check explicit.
func TestAdminConnectorsConfigure_NoSecretsInConfigJsonAfterPOST(t *testing.T) {
	db := setupVaultTestDB(t)
	const validKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	vault, _ := assets.NewVault(db, validKey)
	reg := connectors.NewRegistry()
	_ = reg.Register(newConfigurableConnector(t, "helloasso", configureTestSchema))

	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := chi.NewRouter()
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, vault))

	body := `{"client_id":"id123","client_secret":"NEVER_LOG_THIS","sandbox_mode":false}`
	req := httptest.NewRequest("POST", "/admin/connectors/helloasso/configure", strings.NewReader(body))
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "admin", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("configure code=%d", w.Code)
	}

	var cfgJSON string
	db.QueryRow(`SELECT config_json FROM connectors WHERE id='helloasso'`).Scan(&cfgJSON)
	if strings.Contains(cfgJSON, "NEVER_LOG_THIS") {
		t.Errorf("régression : secret leaké dans config_json : %s", cfgJSON)
	}
}

// TestAdminConnectors_ListShowsAllRegisteredWithStatus : liste contient tous les connectors avec status.
func TestAdminConnectors_ListShowsAllRegisteredWithStatus(t *testing.T) {
	db := setupAdminConnectorsDB(t)
	// Pre-configurer un connector
	if _, err := db.Exec(
		`INSERT INTO connectors(id, enabled, configured_at) VALUES('helloasso', 1, CURRENT_TIMESTAMP), ('stripe', 0, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reg := connectors.NewRegistry()
	_ = reg.Register(&fakeConnector{id: "helloasso", name: "HelloAsso", desc: "Don+adhésion"})
	_ = reg.Register(&fakeConnector{id: "stripe", name: "Stripe", desc: "Paiement carte"})
	_ = reg.Register(&fakeConnector{id: "lydia", name: "Lydia", desc: "Wallet FR"}) // pas en DB

	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	req := httptest.NewRequest("GET", "/admin/connectors", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &auth.User{ID: "admin", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	AdminConnectorsList(deps, reg, nil)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("admin list code=%d, attendu 200", w.Code)
	}

	var views []AdminConnectorsView
	if err := json.Unmarshal(w.Body.Bytes(), &views); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("len(views) = %d, attendu 3", len(views))
	}

	statusByID := map[string]string{}
	for _, v := range views {
		statusByID[v.ID] = v.Status
	}
	if statusByID["helloasso"] != "running" {
		t.Errorf("helloasso status = %q, attendu running", statusByID["helloasso"])
	}
	if statusByID["stripe"] != "disabled" {
		t.Errorf("stripe status = %q, attendu disabled", statusByID["stripe"])
	}
	if statusByID["lydia"] != "not_configured" {
		t.Errorf("lydia status = %q, attendu not_configured", statusByID["lydia"])
	}
}
