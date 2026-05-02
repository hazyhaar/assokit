// CLAUDE:SUMMARY Tests gardiens AdminConnectors — ACL admin, list shows status (M-ASSOKIT-SPRINT2-S1).
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

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
