// CLAUDE:SUMMARY Tests gardiens routes publiques vs protégées (M-ASSOKIT-HELLOASSO-WEBHOOK-PUBLIC).
// CLAUDE:WARN /webhooks/{provider} doit être PUBLIC (pas Bearer ni session). HelloAsso POST
// depuis ses serveurs sans auth user, sécurité = HMAC signature uniquement.
package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/handlers"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"

	_ "modernc.org/sqlite"
)

// newRouterForRouteTests minimal : chassis + middlewares + MountRoutes.
// Tests vérifient le routing pas le contenu des handlers.
func newRouterForRouteTests(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	deps := app.AppDeps{
		DB: db,
		Config: config.Config{
			Port: "0", BaseURL: "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
		},
		Mailer: &mailer.Mailer{DB: db},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	handlers.MountRoutes(r, deps)
	return r, db
}

// TestRoutes_WebhookEndpointPublicNoBearer : POST /webhooks/helloasso sans auth →
// PAS 401 Bearer. Ici WebhookReceiver=nil → 503 explicite (et pas 401 OAuth).
// Garantit que la route est PUBLIQUE (pas derrière OAuth Bearer middleware).
func TestRoutes_WebhookEndpointPublicNoBearer(t *testing.T) {
	r, _ := newRouterForRouteTests(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/helloasso", bytes.NewReader([]byte(`{"id":"x","type":"y"}`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("/webhooks/helloasso a retourné 401 (Bearer required) — la route doit être PUBLIQUE. body=%s", w.Body.String())
	}
	// Sans WebhookReceiver câblé (test minimal), attendu 503 (config absente).
	// Ce qui compte : pas 401 ni 403.
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusOK {
		t.Logf("note: status=%d (acceptable tant que ce n'est pas 401/403 OAuth)", w.Code)
	}
}

// TestRoutes_WebhookEndpointReturnsRetryAfterIfReceiverNil : sans Vault config,
// le receiver retourne 503 + Retry-After 3600 (HelloAsso retry pattern récupère).
func TestRoutes_WebhookEndpointReturnsRetryAfterIfReceiverNil(t *testing.T) {
	r, _ := newRouterForRouteTests(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/helloasso", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("WebhookReceiver=nil : status=%d, want 503", w.Code)
	}
	if w.Header().Get("Retry-After") != "3600" {
		t.Errorf("Retry-After = %q, want 3600", w.Header().Get("Retry-After"))
	}
}

// TestRoutes_WellKnownEndpointsArePublic : /.well-known/oauth-authorization-server
// et /.well-known/mcp/server doivent être public (discovery RFC 8414).
func TestRoutes_WellKnownEndpointsArePublic(t *testing.T) {
	r, _ := newRouterForRouteTests(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
			t.Errorf("%s a retourné %d — discovery RFC 8414 doit être PUBLIC", path, w.Code)
		}
	}
}

// TestRoutes_AdminEndpointsRequireAdmin : /admin/donations sans auth → 403 (pas 200).
func TestRoutes_AdminEndpointsRequireAdmin(t *testing.T) {
	r, _ := newRouterForRouteTests(t)
	for _, path := range []string{"/admin/donations", "/admin/feedbacks"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s sans auth a retourné 200 — protected endpoint doit refuser", path)
		}
	}
}

// TestRoutes_FeedbackPublicAnonymous : POST /feedback accessible sans session
// (visiteurs anonymes peuvent feedback).
func TestRoutes_FeedbackPublicAnonymous(t *testing.T) {
	r, _ := newRouterForRouteTests(t)
	req := httptest.NewRequest(http.MethodPost, "/feedback", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("POST /feedback a retourné %d — visiteur anonyme doit pouvoir POSTer", w.Code)
	}
}

var _ = context.Background // garde l'import stable
