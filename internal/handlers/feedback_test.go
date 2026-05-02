package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func newFeedbackDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{
		DB:     db,
		Logger: slog.Default(),
		Config: config.Config{CookieSecret: []byte("test-secret-32-bytes-00000000000")},
	}
}

func postFeedback(t *testing.T, handler http.Handler, form url.Values, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/feedback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// TestFeedbackHandler_InsertWithCorrectFields vérifie qu'un POST valide insère la ligne.
func TestFeedbackHandler_InsertWithCorrectFields(t *testing.T) {
	deps := newFeedbackDeps(t)
	rl := middleware.NewRateLimiter()
	handler := handleFeedbackPost(deps, rl)

	form := url.Values{
		"message":    {"Ceci est un message valide de test"},
		"page_url":   {"/test-page"},
		"page_title": {"Page de test"},
	}
	w := postFeedback(t, handler, form, "192.168.1.42:54321")

	if w.Code == http.StatusBadRequest {
		t.Fatalf("handler returned 400: %s", w.Body.String())
	}

	var count int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM feedbacks WHERE page_url='/test-page'`).Scan(&count)
	if count != 1 {
		t.Errorf("feedbacks count = %d, want 1", count)
	}

	var ipHash string
	deps.DB.QueryRow(`SELECT ip_hash FROM feedbacks WHERE page_url='/test-page'`).Scan(&ipHash)
	if ipHash == "" {
		t.Error("ip_hash doit être non vide")
	}
	if ipHash == "192.168.1.42" {
		t.Error("ip_hash ne doit pas être l'IP brute")
	}
}

// TestFeedbackHandler_MessageTooShort vérifie qu'un message < 5 chars retourne 400.
func TestFeedbackHandler_MessageTooShort(t *testing.T) {
	deps := newFeedbackDeps(t)
	rl := middleware.NewRateLimiter()
	handler := handleFeedbackPost(deps, rl)

	form := url.Values{"message": {"ab"}, "page_url": {"/"}, "page_title": {""}}
	w := postFeedback(t, handler, form, "10.0.0.1:1234")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}

	var count int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&count)
	if count != 0 {
		t.Errorf("feedbacks count = %d, want 0 (message invalide)", count)
	}
}

// TestFeedbackHandler_HoneypotSilentDrop vérifie que honeypot rempli → 200, 0 INSERT.
func TestFeedbackHandler_HoneypotSilentDrop(t *testing.T) {
	deps := newFeedbackDeps(t)
	rl := middleware.NewRateLimiter()
	handler := handleFeedbackPost(deps, rl)

	form := url.Values{
		"message":    {"Message valide mais honeypot rempli"},
		"page_url":   {"/"},
		"page_title": {""},
		"website":    {"spam@bot.com"},
	}
	w := postFeedback(t, handler, form, "10.0.0.2:1234")

	if w.Code >= 400 {
		t.Errorf("status = %d, want 200 (silent drop honeypot)", w.Code)
	}

	var count int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&count)
	if count != 0 {
		t.Errorf("feedbacks count = %d, want 0 (honeypot drop)", count)
	}
}

// TestFeedbackHandler_RateLimitSilentDrop vérifie que la 4ème tentative est silently drop.
func TestFeedbackHandler_RateLimitSilentDrop(t *testing.T) {
	deps := newFeedbackDeps(t)
	rl := middleware.NewRateLimiter()
	handler := handleFeedbackPost(deps, rl)

	validForm := func(i int) url.Values {
		return url.Values{
			"message":    {"Message valide numéro test " + string(rune('0'+i))},
			"page_url":   {"/"},
			"page_title": {""},
		}
	}

	for i := 0; i < middleware.RateLimitMax; i++ {
		w := postFeedback(t, handler, validForm(i), "10.0.0.3:1234")
		if w.Code == http.StatusBadRequest {
			t.Fatalf("tentative %d: status 400 inattendu: %s", i+1, w.Body.String())
		}
	}

	var count int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&count)
	if count != middleware.RateLimitMax {
		t.Errorf("après %d tentatives valides: count=%d, want %d", middleware.RateLimitMax, count, middleware.RateLimitMax)
	}

	// 4ème tentative → drop silencieux, pas de 429
	w4 := postFeedback(t, handler, validForm(middleware.RateLimitMax), "10.0.0.3:1234")
	if w4.Code == http.StatusTooManyRequests || w4.Code == http.StatusForbidden {
		t.Errorf("rate-limit doit retourner 200 (silent drop), got %d", w4.Code)
	}

	deps.DB.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&count)
	if count != middleware.RateLimitMax {
		t.Errorf("après rate-limit: count=%d, want %d (pas d'INSERT supplémentaire)", count, middleware.RateLimitMax)
	}
}

// TestFeedbackHandler_IPNeverLoggedRaw vérifie que l'IP brute n'apparaît jamais dans les logs.
func TestFeedbackHandler_IPNeverLoggedRaw(t *testing.T) {
	var mu sync.Mutex
	var loggedLines []string

	logHandler := &captureLogHandler{mu: &mu, lines: &loggedLines}
	logger := slog.New(logHandler)

	deps := newFeedbackDeps(t)
	deps.Logger = logger
	rl := middleware.NewRateLimiter()
	handler := handleFeedbackPost(deps, rl)

	const rawIP = "203.0.113.42"
	form := url.Values{
		"message":    {"Message pour tester les logs IP présence"},
		"page_url":   {"/page-test"},
		"page_title": {""},
	}
	postFeedback(t, handler, form, rawIP+":54321")

	mu.Lock()
	defer mu.Unlock()
	for _, line := range loggedLines {
		if strings.Contains(line, rawIP) {
			t.Errorf("IP brute %q trouvée dans les logs : %q", rawIP, line)
		}
	}
}

// captureLogHandler collecte les enregistrements slog pour inspection.
type captureLogHandler struct {
	mu    *sync.Mutex
	lines *[]string
}

func (h *captureLogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler           { return h }
func (h *captureLogHandler) WithGroup(string) slog.Handler                { return h }
func (h *captureLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.lines = append(*h.lines, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		*h.lines = append(*h.lines, a.Key+"="+a.Value.String())
		return true
	})
	return nil
}
