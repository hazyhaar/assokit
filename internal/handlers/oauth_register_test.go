// CLAUDE:SUMMARY Tests gardiens handler /oauth2/register — JSON RFC 7591 + rate-limit + wellknown discovery (M-ASSOKIT-DCR-1).
package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
)

func setupRegisterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE oauth_clients (
			client_id TEXT PRIMARY KEY,
			client_secret_hash TEXT NOT NULL DEFAULT '',
			redirect_uris TEXT NOT NULL DEFAULT '[]',
			grant_types TEXT NOT NULL DEFAULT '[]',
			scopes TEXT NOT NULL DEFAULT '[]',
			owner_user_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	resetDCRRateLimiter()
	return db
}

// TestDCRRegister_HandlerReturns201 : POST valide → 201 + JSON.
func TestDCRRegister_HandlerReturns201(t *testing.T) {
	db := setupRegisterTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	body := `{"client_name":"Claude Web","redirect_uris":["https://claude.ai/cb"],"grant_types":["authorization_code"],"token_endpoint_auth_method":"none","scope":"feedback.create"}`
	req := httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	OAuth2RegisterHandler(deps)(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d body = %s, attendu 201", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	if resp["client_id"] == nil || resp["client_id"] == "" {
		t.Error("client_id absent")
	}
	if resp["client_secret"] != nil && resp["client_secret"] != "" {
		t.Errorf("public client a un secret : %v", resp["client_secret"])
	}
}

// TestDCRRegister_RateLimitedAfter5PerHour : 6e POST même IP → 429.
func TestDCRRegister_RateLimitedAfter5PerHour(t *testing.T) {
	db := setupRegisterTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	body := `{"redirect_uris":["https://x/cb"],"token_endpoint_auth_method":"none"}`

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.42:1234"
		w := httptest.NewRecorder()
		OAuth2RegisterHandler(deps)(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("iter %d : code=%d body=%s", i, w.Code, w.Body.String())
		}
	}

	// 6e POST → 429
	req := httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.42:1234"
	w := httptest.NewRecorder()
	OAuth2RegisterHandler(deps)(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("6e code = %d, attendu 429", w.Code)
	}
}

// TestDCRRegister_InvalidRedirectURIReturns400 : http://malicious refusé.
func TestDCRRegister_InvalidRedirectURIReturns400(t *testing.T) {
	db := setupRegisterTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	body := `{"redirect_uris":["http://malicious.com/cb"],"token_endpoint_auth_method":"none"}`
	req := httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.7:1234"
	w := httptest.NewRecorder()
	OAuth2RegisterHandler(deps)(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400", w.Code)
	}
}

// TestWellKnownOAuthAS_IncludesRegistrationEndpoint : metadata RFC 8414 + DCR.
func TestWellKnownOAuthAS_IncludesRegistrationEndpoint(t *testing.T) {
	deps := app.AppDeps{
		Logger: slog.Default(),
		Config: config.Config{BaseURL: "https://nonpossumus.eu"},
	}
	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	WellKnownOAuthAuthorizationServer(deps)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`"registration_endpoint":"https://nonpossumus.eu/oauth2/register"`,
		`"code_challenge_methods_supported":["S256"]`,
		`"authorization_endpoint":"https://nonpossumus.eu/oauth2/authorize"`,
		`"token_endpoint":"https://nonpossumus.eu/oauth2/token"`,
		`"token_endpoint_auth_methods_supported"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metadata absent : %q\nbody: %s", want, body)
		}
	}
}

// TestDCRRegister_PersistsInDBViaHandler : end-to-end POST → row in oauth_clients.
func TestDCRRegister_PersistsInDBViaHandler(t *testing.T) {
	db := setupRegisterTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	body := `{"redirect_uris":["https://example.org/cb"],"token_endpoint_auth_method":"none","client_name":"App X"}`
	req := httptest.NewRequest("POST", "/oauth2/register", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.11:1234"
	w := httptest.NewRecorder()
	OAuth2RegisterHandler(deps)(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	clientID, _ := resp["client_id"].(string)

	var redirects string
	err := db.QueryRow(`SELECT redirect_uris FROM oauth_clients WHERE client_id = ?`, clientID).Scan(&redirects)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !strings.Contains(redirects, "example.org") {
		t.Errorf("redirect_uris stored = %q", redirects)
	}
}
