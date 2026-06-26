// CLAUDE:SUMMARY Tests gardiens widget feedback — restrict identified + CSS visibility (M-ASSOKIT-FEEDBACK-WIDGET-CSS-MISSING).
package handlers

import (
	"context"
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
	"github.com/hazyhaar/assokit/pkg/eventsink"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
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
// TestFeedback_AnonymousPOSTAccepted : le feedback est un canal de debug présent sur
// CHAQUE page, y compris les pages anonymes (login, récupération de mot de passe) où
// l'utilisateur n'est pas connecté et rencontre le plus de friction. Un POST anonyme
// valide est donc accepté et inséré (le record ne référence aucun utilisateur ;
// honeypot + rate-limit protègent du spam). Régression auditée 2026-06-13.
func TestFeedback_AnonymousPOSTAccepted(t *testing.T) {
	db := setupFeedbackWidgetTestDB(t)
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
	}
	rl := middleware.NewRateLimiter()
	body := strings.NewReader("message=Bug+sur+la+page+de+mot+de+passe&page_url=/reset")
	req := httptest.NewRequest("POST", "/feedback", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.9:1234"
	w := httptest.NewRecorder()
	handleFeedbackPost(setLoggerNoop(deps), rl)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("anonymous POST code = %d, attendu 200 (accepté) body=%s", w.Code, w.Body.String())
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&n)
	if n != 1 {
		t.Errorf("anonymous POST INSERT count = %d, attendu 1", n)
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
		&identity.User{ID: "u-1", Email: "u@x.com"}))
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

// captureSink capture les événements émis (test du debug-channel feedback->bus).
type captureSink struct{ events []eventsink.Event }

func (c *captureSink) Emit(_ context.Context, e eventsink.Event) error {
	c.events = append(c.events, e)
	return nil
}

// TestFeedback_EmitsEvent : un feedback posté émet feedback.created vers le Sink.
func TestFeedback_EmitsEvent(t *testing.T) {
	db := setupFeedbackWidgetTestDB(t)
	sink := &captureSink{}
	deps := app.AppDeps{
		DB: db, Logger: slog.Default(),
		Config:    config.Config{CookieSecret: []byte("0123456789abcdef0123456789abcdef")},
		EventSink: sink,
	}
	rl := middleware.NewRateLimiter()
	body := strings.NewReader("message=Bug+sur+la+page&page_url=/forum")
	req := httptest.NewRequest("POST", "/feedback", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.9:1234"
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &identity.User{ID: "u-1", Email: "u@x.com"}))
	w := httptest.NewRecorder()
	handleFeedbackPost(deps, rl)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if len(sink.events) != 1 || sink.events[0].Type != "feedback.created" {
		t.Fatalf("événements émis = %+v, attendu 1x feedback.created", sink.events)
	}
	if sink.events[0].Payload["page_url"] != "/forum" {
		t.Fatalf("payload page_url = %v", sink.events[0].Payload["page_url"])
	}
}
