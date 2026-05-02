// CLAUDE:SUMMARY Tests gardiens /oauth2/authorize — session check + PKCE mandatory + redirect login (M-ASSOKIT-DCR-3).
package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func setupAuthorizeTestDB(t *testing.T) *sql.DB {
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
	return db
}

func seedOAuthClient(t *testing.T, db *sql.DB, clientID, secretHash string, redirectURIs []string) {
	t.Helper()
	redirectsJSON := `["` + strings.Join(redirectURIs, `","`) + `"]`
	if _, err := db.Exec(
		`INSERT INTO oauth_clients(client_id, client_secret_hash, redirect_uris) VALUES(?, ?, ?)`,
		clientID, secretHash, redirectsJSON,
	); err != nil {
		t.Fatalf("seed client: %v", err)
	}
}

// TestAuthorize_NoSessionRedirectsToLogin : pas de user → 302 /login avec return_url.
func TestAuthorize_NoSessionRedirectsToLogin(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedOAuthClient(t, db, "client-X", "", []string{"https://claude.ai/cb"})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-X")
	q.Set("redirect_uri", "https://claude.ai/cb")
	q.Set("scope", "feedback.create")
	q.Set("state", "xyz")
	q.Set("code_challenge", "abc123challenge")
	q.Set("code_challenge_method", "S256")

	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d body=%s, attendu 302", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?return_url=") {
		t.Errorf("Location = %q, attendu /login?return_url=...", loc)
	}
	if !strings.Contains(loc, "client_id") {
		t.Errorf("return_url ne contient pas l'URL authorize complète : %q", loc)
	}
}

// TestAuthorize_SessionRendersConsentPage : session présente → 200 avec page consent.
func TestAuthorize_SessionRendersConsentPage(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	seedOAuthClient(t, db, "client-X", "", []string{"https://claude.ai/cb"})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-X")
	q.Set("redirect_uri", "https://claude.ai/cb")
	q.Set("scope", "feedback.create forum.post.create")
	q.Set("state", "xyz")
	q.Set("code_challenge", "abc")
	q.Set("code_challenge_method", "S256")

	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "u-1", Email: "u@x.com"}))
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s, attendu 200 (consent rendered)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"Créer un nouveau feedback en votre nom",
		"Publier un message sur le forum en votre nom",
		"Autoriser",
		"Refuser",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page manque %q", want)
		}
	}
}

// TestAuthorize_PublicClientRequiresPKCE : client public sans code_challenge → 400.
func TestAuthorize_PublicClientRequiresPKCE(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedOAuthClient(t, db, "client-public", "", []string{"https://x/cb"}) // secret_hash vide = public

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-public")
	q.Set("redirect_uri", "https://x/cb")
	// code_challenge ABSENT volontairement
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (PKCE manquant)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "code_challenge") {
		t.Errorf("body ne mentionne pas code_challenge : %s", w.Body.String())
	}
}

// TestAuthorize_ConfidentialClientPKCEOptional : confidential client OK sans code_challenge.
func TestAuthorize_ConfidentialClientPKCEOptional(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	// secret_hash non vide = confidential
	seedOAuthClient(t, db, "client-conf", "fakehash", []string{"https://x/cb"})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-conf")
	q.Set("redirect_uri", "https://x/cb")
	q.Set("scope", "openid")
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "u-1"}))
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("confidential sans PKCE code = %d, attendu 200 (PKCE optional pour confidential)", w.Code)
	}
}

// TestAuthorize_UnknownClientID : client inconnu → 400.
func TestAuthorize_UnknownClientID(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-inexistant")
	q.Set("redirect_uri", "https://x/cb")
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (client inconnu)", w.Code)
	}
}

// TestAuthorize_RedirectURINotInWhitelist : redirect_uri pas dans la whitelist du client → 400.
func TestAuthorize_RedirectURINotInWhitelist(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedOAuthClient(t, db, "client-X", "", []string{"https://allowed.com/cb"})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-X")
	q.Set("redirect_uri", "https://attacker.com/cb")
	q.Set("code_challenge", "abc")
	q.Set("code_challenge_method", "S256")
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (redirect_uri attaquant)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "redirect_uri") {
		t.Errorf("body ne cite pas redirect_uri : %s", w.Body.String())
	}
}

// TestAuthorize_InvalidResponseType : response_type != code → 400.
func TestAuthorize_InvalidResponseType(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	q := url.Values{}
	q.Set("response_type", "token") // implicit grant deprecated
	q.Set("client_id", "x")
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400", w.Code)
	}
}

// TestAuthorize_PKCEMethodMustBeS256 : code_challenge_method=plain → 400.
func TestAuthorize_PKCEMethodMustBeS256(t *testing.T) {
	db := setupAuthorizeTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedOAuthClient(t, db, "client-X", "", []string{"https://x/cb"})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-X")
	q.Set("redirect_uri", "https://x/cb")
	q.Set("code_challenge", "challenge-plaintext")
	q.Set("code_challenge_method", "plain") // refusé
	req := httptest.NewRequest("GET", "/oauth2/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	OAuth2AuthorizeHandler(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (méthode plain refusée)", w.Code)
	}
}
