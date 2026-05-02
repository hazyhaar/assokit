// CLAUDE:SUMMARY Tests gardiens magic link login (M-ASSOKIT-DCR-2).
package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/mailer"
)

func setupMagicLinkDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '');
		CREATE TABLE login_magic_tokens (
			token TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			user_id TEXT,
			return_url TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL,
			used_at TEXT,
			ip_hash TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE email_outbox (
			id TEXT PRIMARY KEY,
			to_addr TEXT NOT NULL, subject TEXT NOT NULL,
			body_text TEXT NOT NULL DEFAULT '', body_html TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			retry_after TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			sent_at TEXT
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	resetMagicRateLimiter()
	return db
}

func depsWithMailer(db *sql.DB) app.AppDeps {
	logger := slog.Default()
	ml := &mailer.Mailer{DB: db, Logger: logger, From: "noreply@x.com"}
	return app.AppDeps{
		DB: db, Logger: logger, Mailer: ml,
		Config: config.Config{
			BaseURL:      "https://nps.test",
			CookieSecret: []byte("0123456789abcdef0123456789abcdef"),
		},
	}
}

// TestMagicLink_SubmitGeneratesTokenAndQueuesEmail : POST email valide → token DB + email outbox.
func TestMagicLink_SubmitGeneratesTokenAndQueuesEmail(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	form := url.Values{}
	form.Set("email", "alice@example.org")
	form.Set("return_url", "/oauth2/authorize?x=1")
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	LoginMagicSubmit(deps)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}

	var token, email, returnURL string
	err := db.QueryRow(`SELECT token, email, return_url FROM login_magic_tokens WHERE email = ?`, "alice@example.org").Scan(&token, &email, &returnURL)
	if err != nil {
		t.Fatalf("token absent : %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token len = %d, attendu 64 (32 bytes hex)", len(token))
	}
	if returnURL != "/oauth2/authorize?x=1" {
		t.Errorf("return_url = %q", returnURL)
	}

	// Email queued.
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM email_outbox WHERE to_addr = ?`, "alice@example.org").Scan(&n)
	if n != 1 {
		t.Errorf("email_outbox count = %d, attendu 1", n)
	}

	// Body HTML contient le callback URL avec le token.
	var bodyHTML string
	db.QueryRow(`SELECT body_html FROM email_outbox WHERE to_addr = ?`, "alice@example.org").Scan(&bodyHTML)
	if !strings.Contains(bodyHTML, "/login/callback?token="+token) {
		t.Errorf("body HTML manque callback URL avec token : %s", bodyHTML)
	}
}

// TestMagicLink_RateLimitedAfter3PerWindow : 4e POST IP même → 429.
func TestMagicLink_RateLimitedAfter3PerWindow(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	for i := 0; i < 3; i++ {
		form := url.Values{}
		form.Set("email", "rate@x.com")
		req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.99:1234"
		w := httptest.NewRecorder()
		LoginMagicSubmit(deps)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d code = %d", i, w.Code)
		}
	}

	// 4e → 429
	req := httptest.NewRequest("POST", "/login", strings.NewReader("email=rate@x.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.99:1234"
	w := httptest.NewRecorder()
	LoginMagicSubmit(deps)(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("4e POST code = %d, attendu 429", w.Code)
	}
}

// TestMagicLink_CallbackValidTokenSetsSession : GET /login/callback?token=... → session cookie + redirect.
func TestMagicLink_CallbackValidTokenSetsSession(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	// Préparer un token valide directement en DB.
	_, _ = db.Exec(`INSERT INTO users(id, email) VALUES('u-bob', 'bob@x.com')`)
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Format("2006-01-02 15:04:05")
	token := strings.Repeat("ab", 32) // 64 chars hex
	_, err := db.Exec(`
		INSERT INTO login_magic_tokens(token, email, user_id, return_url, expires_at)
		VALUES (?, ?, 'u-bob', '/dashboard', ?)
	`, token, "bob@x.com", expiresAt)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest("GET", "/login/callback?token="+token, nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	if w.Header().Get("Location") != "/dashboard" {
		t.Errorf("Location = %q, attendu /dashboard", w.Header().Get("Location"))
	}
	// Session cookie présent.
	cookies := w.Result().Cookies()
	hasSession := false
	for _, c := range cookies {
		if strings.Contains(c.Name, "session") {
			hasSession = true
			break
		}
	}
	if !hasSession {
		t.Errorf("aucun session cookie set, cookies = %v", cookies)
	}

	// Token marqué used.
	var usedAt sql.NullString
	db.QueryRow(`SELECT used_at FROM login_magic_tokens WHERE token = ?`, token).Scan(&usedAt)
	if !usedAt.Valid {
		t.Error("used_at non posé après callback")
	}
}

// TestMagicLink_CallbackExpiredToken : token >15min → erreur.
func TestMagicLink_CallbackExpiredToken(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	expiresAt := time.Now().UTC().Add(-1 * time.Minute).Format("2006-01-02 15:04:05")
	token := strings.Repeat("ef", 32)
	db.Exec(`INSERT INTO login_magic_tokens(token, email, expires_at) VALUES (?, ?, ?)`, token, "exp@x.com", expiresAt) //nolint:errcheck

	req := httptest.NewRequest("GET", "/login/callback?token="+token, nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (expired)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "expir") {
		t.Errorf("body ne mentionne pas expiration : %s", w.Body.String())
	}
}

// TestMagicLink_CallbackUsedToken : token déjà used → refusé (anti-replay).
func TestMagicLink_CallbackUsedToken(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	expiresAt := time.Now().UTC().Add(5 * time.Minute).Format("2006-01-02 15:04:05")
	token := strings.Repeat("cd", 32)
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	db.Exec(`INSERT INTO login_magic_tokens(token, email, expires_at, used_at) VALUES (?, ?, ?, ?)`,
		token, "used@x.com", expiresAt, now) //nolint:errcheck

	req := httptest.NewRequest("GET", "/login/callback?token="+token, nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400 (replay)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "déjà") {
		t.Errorf("body ne mentionne pas 'déjà utilisé' : %s", w.Body.String())
	}
}

// TestMagicLink_CallbackUnknownToken : token random → erreur.
func TestMagicLink_CallbackUnknownToken(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)
	req := httptest.NewRequest("GET", "/login/callback?token="+strings.Repeat("00", 32), nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400", w.Code)
	}
}

// TestMagicLink_CallbackFirstTimeCreatesUser : token user_id NULL + first time → user créé.
func TestMagicLink_CallbackFirstTimeCreatesUser(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	expiresAt := time.Now().UTC().Add(10 * time.Minute).Format("2006-01-02 15:04:05")
	token := strings.Repeat("12", 32)
	db.Exec(`INSERT INTO login_magic_tokens(token, email, return_url, expires_at) VALUES (?, ?, ?, ?)`,
		token, "newbie@x.com", "/", expiresAt) //nolint:errcheck

	req := httptest.NewRequest("GET", "/login/callback?token="+token, nil)
	w := httptest.NewRecorder()
	LoginMagicCallback(deps)(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "newbie@x.com").Scan(&n)
	if n != 1 {
		t.Errorf("user créé count = %d, attendu 1", n)
	}
}

// TestMagicLink_InvalidEmailReturns400 : POST email mal formé → 400.
func TestMagicLink_InvalidEmailReturns400(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	form := url.Values{}
	form.Set("email", "pasunemail")
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.5:1234"
	w := httptest.NewRecorder()
	LoginMagicSubmit(deps)(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, attendu 400", w.Code)
	}
}

// TestMagicLink_NoIPRawLogged : POST loggue ip_hash, jamais l'IP brute.
func TestMagicLink_NoIPRawLogged(t *testing.T) {
	db := setupMagicLinkDB(t)
	deps := depsWithMailer(db)

	form := url.Values{}
	form.Set("email", "iptest@x.com")
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.99:9999"
	w := httptest.NewRecorder()
	LoginMagicSubmit(deps)(w, req)

	// SELECT ip_hash : doit être présent + pas IP brute.
	var ipHash string
	db.QueryRow(`SELECT ip_hash FROM login_magic_tokens WHERE email = ?`, "iptest@x.com").Scan(&ipHash)
	if ipHash == "" {
		t.Error("ip_hash vide")
	}
	if strings.Contains(ipHash, "203.0.113.99") {
		t.Errorf("ip_hash leak IP brute : %q", ipHash)
	}
}

// _ = context : keep import.
var _ = context.Background
