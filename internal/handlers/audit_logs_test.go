// CLAUDE:SUMMARY Tests gardiens audit logs M-ASSOKIT-AUDIT-FIX-1 — signup/feedback/mcp logs + no PII leak.
package handlers

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	appMiddleware "github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func setupAuditTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT UNIQUE, password_hash TEXT, display_name TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE user_roles (user_id TEXT, role_id TEXT, PRIMARY KEY(user_id, role_id));
		CREATE TABLE signups (id TEXT PRIMARY KEY, email TEXT, display_name TEXT, profile TEXT, fields_json TEXT, ip_hash TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE activation_tokens (token TEXT PRIMARY KEY, user_id TEXT, expires_at TEXT, used_at TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE feedbacks (id TEXT PRIMARY KEY, page_url TEXT, page_title TEXT, message TEXT, ip_hash TEXT, user_agent TEXT, locale TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestSignupFlow_LogsAttemptCreated : POST signup → slog contient signup_attempt + signup_created avec req_id non vide.
func TestSignupFlow_LogsAttemptCreated(t *testing.T) {
	db := setupAuditTestDB(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}

	r := chi.NewRouter()
	r.Use(appMiddleware.RequestID)
	r.Post("/adherer/{profil}", handleSignupSubmit(deps))

	form := url.Values{}
	form.Set("email", "alice@example.org")
	form.Set("prenom", "Alice")
	req := httptest.NewRequest("POST", "/adherer/adherent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	output := buf.String()
	if !strings.Contains(output, "signup_attempt") {
		t.Errorf("output ne contient pas 'signup_attempt' : %s", output)
	}
	if !strings.Contains(output, "signup_created") {
		t.Errorf("output ne contient pas 'signup_created' : %s", output)
	}
	// req_id non-vide
	reqIDRegex := regexp.MustCompile(`"req_id":"[a-f0-9-]{30,}"`)
	if !reqIDRegex.MatchString(output) {
		t.Errorf("aucun req_id valide trouvé : %s", output)
	}
	// Email en clair NE DOIT PAS apparaître
	if strings.Contains(output, "alice@example.org") {
		t.Errorf("output contient l'email en clair (PII leak) : %s", output)
	}
}

// TestFeedbackRateLimitLogged : 4 POSTs en <5min → slog contient feedback_rate_limited.
func TestFeedbackRateLimitLogged(t *testing.T) {
	db := setupAuditTestDB(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}

	rl := appMiddleware.NewRateLimiter()
	r := chi.NewRouter()
	r.Use(appMiddleware.RequestID)
	r.Post("/feedback", handleFeedbackPost(deps, rl))

	postFeedback := func(msg string) int {
		form := url.Values{}
		form.Set("message", msg)
		form.Set("page_url", "/test")
		req := httptest.NewRequest("POST", "/feedback", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.1:12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	for i := 0; i < 4; i++ {
		postFeedback("Hello world from test " + string(rune('A'+i)))
	}

	output := buf.String()
	if !strings.Contains(output, "feedback_rate_limited") {
		t.Errorf("output ne contient pas 'feedback_rate_limited' après 4 POSTs : %s", output)
	}
	// IP brute NE DOIT PAS apparaître
	if strings.Contains(output, "192.0.2.1") {
		t.Errorf("output contient l'IP brute (RGPD leak) : %s", output)
	}
}

// TestSlogNoIPRawLeak : smoke (signup + feedback) → 0 IP brute (hors 127.x) dans logs structurés.
// Vérifie que l'invariant rule RGPD-IP-NEVER-RAW-IN-LOGS tient.
func TestSlogNoIPRawLeak(t *testing.T) {
	db := setupAuditTestDB(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}

	rl := appMiddleware.NewRateLimiter()
	r := chi.NewRouter()
	r.Use(appMiddleware.RequestID)
	r.Post("/adherer/{profil}", handleSignupSubmit(deps))
	r.Post("/feedback", handleFeedbackPost(deps, rl))

	doRequest := func(path string, form url.Values, ip string) {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	// Signup
	signupForm := url.Values{}
	signupForm.Set("email", "bob@example.org")
	signupForm.Set("prenom", "Bob")
	doRequest("/adherer/adherent", signupForm, "203.0.113.45:6789")

	// Feedback
	fbForm := url.Values{}
	fbForm.Set("message", "Hello, ceci est un feedback de test.")
	fbForm.Set("page_url", "/test")
	doRequest("/feedback", fbForm, "198.51.100.10:5555")

	output := buf.String()

	// Regex IPv4 hors 127.x (loopback admis pour tests)
	ipRegex := regexp.MustCompile(`\b(?:1[0-9]{2}|2[0-4][0-9]|25[0-5]|[1-9]?[0-9])(?:\.(?:1[0-9]{2}|2[0-4][0-9]|25[0-5]|[1-9]?[0-9])){3}\b`)
	matches := ipRegex.FindAllString(output, -1)
	for _, m := range matches {
		if !strings.HasPrefix(m, "127.") {
			t.Errorf("IP brute trouvée dans logs : %q (full output: %s)", m, output)
		}
	}

	// Pas d'email non plus
	if strings.Contains(output, "bob@example.org") {
		t.Errorf("email en clair leaké : %s", output)
	}
}
