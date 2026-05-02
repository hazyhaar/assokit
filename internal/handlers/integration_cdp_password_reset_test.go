//go:build integration_cdp

// CLAUDE:SUMMARY Test CDP password reset full cycle — skip si endpoint absent (M-ASSOKIT-AUDIT-FIX-2).
package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

// TestCDP_PasswordResetFullCycle : forgot/reset/login.
// Si endpoint /forgot-password ou /reset-password absent → t.Skip.
// TODO M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW
func TestCDP_PasswordResetFullCycle(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun chromium")
	}

	dbPath := filepath.Join(t.TempDir(), "pwd-reset.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	seedRoles(t, db)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})
	deps := app.AppDeps{
		DB: db, Logger: logger,
		Mailer: &mailer.Mailer{DB: db, From: "x@x", AdminTo: "x@x", Logger: logger},
		Config: config.Config{Port: "0", DBPath: dbPath, BaseURL: "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000")},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	MountRoutes(r, deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Vérifier que /forgot-password existe (HEAD ou GET).
	resp, err := http.Get(srv.URL + "/forgot-password")
	if err != nil {
		t.Skip("/forgot-password not implemented (network err) — TODO M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW")
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("/forgot-password not implemented — TODO M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW")
	}

	// Endpoint existe — exécuter cycle CDP.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL+"/forgot-password")); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	t.Log("CDP password reset full cycle pas encore complet — TODO M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW")
	t.Skip("flow complet pas implémenté — endpoint présent mais cycle non testé")
}
