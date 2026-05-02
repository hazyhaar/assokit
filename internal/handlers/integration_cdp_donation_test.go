//go:build integration_cdp

// CLAUDE:SUMMARY Test CDP — bouton don sur /soutenir → URL HelloAsso (M-ASSOKIT-AUDIT-FIX-2).
package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestCDP_DonationButtonNavigatesToHelloAsso vérifie que /soutenir contient un lien
// (iframe ou bouton) pointant vers l'URL HelloAsso configurée.
func TestCDP_DonationButtonNavigatesToHelloAsso(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé")
	}

	dbPath := filepath.Join(t.TempDir(), "donate-cdp.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	seedRoles(t, db)
	if err := SeedNodes(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})

	const helloURL = "https://www.helloasso.com/associations/test/dons"
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Mailer: &mailer.Mailer{DB: db, From: "x@x", AdminTo: "x@x", Logger: logger},
		Config: config.Config{
			Port: "0", DBPath: dbPath, BaseURL: "http://localhost",
			CookieSecret:           []byte("test-cookie-secret-32bytes-padded000"),
			HelloassoDonURL:        helloURL,
			HelloassoCotisationURL: "https://www.helloasso.com/associations/test/adhesions",
		},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	MountRoutes(r, deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	var html string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/soutenir"),
		chromedp.WaitVisible(`.page-donate`, chromedp.ByQuery),
		chromedp.OuterHTML(`.page-donate`, &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate /soutenir: %v", err)
	}
	if !strings.Contains(html, helloURL) {
		t.Errorf("URL HelloAsso %q absente de /soutenir : %s", helloURL, html[:min(len(html), 400)])
	}
}
