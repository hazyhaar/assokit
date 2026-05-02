// CLAUDE:SUMMARY Tests gardiens widget feedback — restrict identified + CSS visibility (M-ASSOKIT-FEEDBACK-WIDGET-CSS-MISSING).
package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// TestCSS_FeedbackFabHasPositionFixed : test gardien anti-régression du faux F2.
// Le widget doit être visible : CSS contient .feedback-fab avec position:fixed.
// Si CSS disparaît à nouveau, ce test rouge immédiat.
func TestCSS_FeedbackFabHasPositionFixed(t *testing.T) {
	candidates := []string{
		"../../static/css/horui.css",
		"static/css/horui.css",
	}
	var content []byte
	var found string
	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		b, err := os.ReadFile(abs)
		if err == nil {
			content = b
			found = abs
			break
		}
	}
	if content == nil {
		t.Skip("horui.css introuvable depuis le test (chemin relatif)")
	}
	css := string(content)

	if !strings.Contains(css, ".feedback-fab") {
		t.Errorf("horui.css (%s) manque .feedback-fab", found)
	}
	// Cherche position: fixed dans le bloc .feedback-fab.
	idx := strings.Index(css, ".feedback-fab")
	if idx < 0 {
		t.Fatal("feedback-fab missing")
	}
	// Capture les 500 bytes suivants
	end := idx + 500
	if end > len(css) {
		end = len(css)
	}
	block := css[idx:end]
	if !strings.Contains(block, "position: fixed") && !strings.Contains(block, "position:fixed") {
		t.Errorf(".feedback-fab manque position:fixed (widget invisible !) — block: %s", block[:min(len(block), 200)])
	}
	if !strings.Contains(block, "z-index") {
		t.Errorf(".feedback-fab manque z-index (widget potentiellement caché par autres éléments)")
	}
}

func setupFeedbackWidgetTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE feedbacks (
			id TEXT PRIMARY KEY, page_url TEXT, page_title TEXT, message TEXT,
			ip_hash TEXT, user_agent TEXT, locale TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestFeedback_AnonymousPOSTRedirectedToLogin : POST /feedback sans session → 303 /login.
func TestFeedback_AnonymousPOSTRedirectedToLogin(t *testing.T) {
	db := setupFeedbackWidgetTestDB(t)
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	rl := middleware.NewRateLimiter()
	body := strings.NewReader("message=hello&page_url=/")
	req := httptest.NewRequest("POST", "/feedback", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handleFeedbackPost(setLoggerNoop(deps), rl)(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("anonymous POST code = %d, attendu 303 (redirect /login)", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Errorf("Location = %q, attendu /login*", loc)
	}

	// Aucune row INSERT.
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&n)
	if n != 0 {
		t.Errorf("anonymous POST a créé une row : count=%d", n)
	}
}

// TestFeedback_AuthenticatedPOSTAccepted : POST avec session → INSERT row (anonyme côté table).
func TestFeedback_AuthenticatedPOSTAccepted(t *testing.T) {
	db := setupFeedbackWidgetTestDB(t)
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	deps = setLoggerNoop(deps)
	rl := middleware.NewRateLimiter()
	body := strings.NewReader("message=Salut+ceci+est+un+message+de+test&page_url=/")
	req := httptest.NewRequest("POST", "/feedback", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.5:1234"
	req = req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "u-1", Email: "u@x.com"}))
	w := httptest.NewRecorder()
	handleFeedbackPost(deps, rl)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("auth POST code = %d body=%s", w.Code, w.Body.String())
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&n)
	if n != 1 {
		t.Errorf("INSERT count = %d, attendu 1", n)
	}
}

// setLoggerNoop : helper pour tests sans logger (slog.Default ok mais on évite stdout pollution).
func setLoggerNoop(deps app.AppDeps) app.AppDeps {
	if deps.Logger == nil {
		// utilise slog default qui écrit en stderr — ok pour tests.
	}
	return deps
}

