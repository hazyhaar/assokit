// CLAUDE:SUMMARY Tests gardiens consent submit + render — approve/deny flow + CSRF + scopes FR (M-ASSOKIT-DCR-4).
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	authpages "github.com/hazyhaar/assokit/pkg/horui/auth/pages"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func setupConsentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE oauth_authcodes (
			code TEXT PRIMARY KEY,
			auth_req_id TEXT NOT NULL,
			client_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			scopes TEXT NOT NULL DEFAULT '[]',
			redirect_uri TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT,
			code_challenge TEXT NOT NULL DEFAULT '',
			code_challenge_method TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func userCtx(req *http.Request) *http.Request {
	return req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "u-test", Email: "u@test.com"}))
}

// TestConsentSubmit_ApproveGeneratesCode : approve → 302 vers redirect_uri avec code param.
func TestConsentSubmit_ApproveGeneratesCode(t *testing.T) {
	db := setupConsentTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	form := url.Values{}
	form.Set("decision", "approve")
	form.Set("auth_request_id", "req-1")
	form.Set("redirect_uri", "https://claude.ai/cb")
	form.Set("state", "xyz")
	form.Set("client_id", "client-A")
	form.Set("scope", "feedback.create forum.post.create")

	req := userCtx(httptest.NewRequest("POST", "/oauth2/consent", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	OAuth2ConsentSubmit(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d body=%s, attendu 302", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://claude.ai/cb?code=") {
		t.Errorf("Location = %q", loc)
	}
	if !strings.Contains(loc, "state=xyz") {
		t.Errorf("state non propagé : %q", loc)
	}

	// Vérifier que oauth_authcodes a une row.
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM oauth_authcodes WHERE user_id='u-test'`).Scan(&n)
	if n != 1 {
		t.Errorf("oauth_authcodes count = %d, attendu 1", n)
	}
}

// TestConsentSubmit_DenyReturnsAccessDeniedError : deny → 302 avec error=access_denied.
func TestConsentSubmit_DenyReturnsAccessDeniedError(t *testing.T) {
	db := setupConsentTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	form := url.Values{}
	form.Set("decision", "deny")
	form.Set("redirect_uri", "https://claude.ai/cb")
	form.Set("state", "xyz")
	req := userCtx(httptest.NewRequest("POST", "/oauth2/consent", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	OAuth2ConsentSubmit(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=access_denied") {
		t.Errorf("Location ne contient pas error=access_denied : %q", loc)
	}

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM oauth_authcodes`).Scan(&n)
	if n != 0 {
		t.Errorf("deny ne devrait pas créer d'authcode, n=%d", n)
	}
}

// TestConsentSubmit_CodeChallengeStoredInAuthcode : code_challenge persisté.
func TestConsentSubmit_CodeChallengeStoredInAuthcode(t *testing.T) {
	db := setupConsentTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	form := url.Values{}
	form.Set("decision", "approve")
	form.Set("auth_request_id", "req-2")
	form.Set("redirect_uri", "https://claude.ai/cb")
	form.Set("state", "s2")
	form.Set("client_id", "client-B")
	form.Set("scope", "openid")
	form.Set("code_challenge", "abc123challenge")
	form.Set("code_challenge_method", "S256")

	req := userCtx(httptest.NewRequest("POST", "/oauth2/consent", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	OAuth2ConsentSubmit(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}

	var stored, method string
	err := db.QueryRow(`SELECT code_challenge, code_challenge_method FROM oauth_authcodes WHERE user_id='u-test'`).Scan(&stored, &method)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if stored != "abc123challenge" {
		t.Errorf("code_challenge = %q, attendu abc123challenge", stored)
	}
	if method != "S256" {
		t.Errorf("code_challenge_method = %q", method)
	}
}

// TestConsentSubmit_NoSessionRedirectsLogin : pas de user context → redirect /login.
func TestConsentSubmit_NoSessionRedirectsLogin(t *testing.T) {
	db := setupConsentTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	req := httptest.NewRequest("POST", "/oauth2/consent", strings.NewReader("decision=approve"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	OAuth2ConsentSubmit(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("code = %d, attendu 302 (redirect /login)", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q, attendu /login", w.Header().Get("Location"))
	}
}

// TestConsentSubmit_InvalidDecisionReturns400 : decision != approve|deny → 400.
func TestConsentSubmit_InvalidDecisionReturns400(t *testing.T) {
	db := setupConsentTestDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	form := url.Values{}
	form.Set("decision", "abstain")
	form.Set("redirect_uri", "https://claude.ai/cb")
	req := userCtx(httptest.NewRequest("POST", "/oauth2/consent", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	OAuth2ConsentSubmit(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400", w.Code)
	}
}

// TestConsentPage_RendersScopesInFrench : Render + grep libellés FR présents.
func TestConsentPage_RendersScopesInFrench(t *testing.T) {
	props := authpages.ConsentProps{
		ClientName:    "Claude Web",
		AuthRequestID: "req-x",
		CSRFToken:     "csrf-123",
		ScopesGranted: authpages.LibellesScope([]string{"feedback.create", "forum.post.create"}),
		RedirectURI:   "https://claude.ai/cb",
		State:         "abc",
	}
	var buf bytes.Buffer
	if err := authpages.Consent(props).Render(context.Background(), &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		"Créer un nouveau feedback en votre nom",
		"Publier un message sur le forum en votre nom",
		"Elle pourra :",
		"Elle ne pourra PAS :",
		"Autoriser",
		"Refuser",
		"révoquer",
		"Claude Web",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML manque %q", want)
		}
	}
}

// TestConsentPage_UnknownScopeShowsFallback : scope inconnu → "Action technique : X" rendu.
func TestConsentPage_UnknownScopeShowsFallback(t *testing.T) {
	props := authpages.ConsentProps{
		ClientName:    "TestApp",
		ScopesGranted: authpages.LibellesScope([]string{"unmapped.future_scope"}),
		RedirectURI:   "https://x/cb",
	}
	var buf bytes.Buffer
	authpages.Consent(props).Render(context.Background(), &buf) //nolint:errcheck
	html := buf.String()
	if !strings.Contains(html, "Action technique") || !strings.Contains(html, "unmapped.future_scope") {
		t.Errorf("fallback non rendu : %s", html)
	}
}
