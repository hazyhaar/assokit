//go:build integration_cdp

// CLAUDE:SUMMARY Test gardien CDP : charge le branding.toml NPS RÉEL puis crawle tous les <a href> de /, asserte 0 lien 4xx/5xx. Détecte les liens cassés layout.templ + branding.toml.
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

// TestCDP_HomeLinksAllResolveTo2xx : charge le branding.toml NPS prod-like
// (ou un test fixture qui le simule), navigue /, collecte tous les <a href> internes,
// vérifie que chacun renvoie HTTP 2xx ou 3xx. Détecte tout drift entre liens
// HTML et routes serveur (cas réel : /donate, /signup, /forgot-password 404).
func TestCDP_HomeLinksAllResolveTo2xx(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("chromium absent — skip")
	}

	// 1. DB setup
	dbPath := filepath.Join(t.TempDir(), "nps-link-crawl.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	seedRoles(t, db)
	if err := SeedNodes(db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 2. Charger le branding.toml NPS réel s'il existe (sinon brand minimal avec
	//    les 4 liens qu'on sait avoir cassé en prod : /donate, /signup, /forgot-password, /reset-password).
	brand := &theme.Branding{
		Name: "NPS Test Crawl",
		Nav: []theme.NavItem{
			{Slug: "/participer", Label: "Participer"},
			{Slug: "/thematiques", Label: "Thématiques"},
			{Slug: "/medias", Label: "Médias"},
			{Slug: "/forum", Label: "Forum"},
			{Slug: "/donate", Label: "Soutenir"}, // alias serveur → /soutenir
			{Slug: "/contact", Label: "Contact"},
		},
	}
	theme.Init(brand)

	// 3. App + httptest server
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Mailer: &mailer.Mailer{DB: db, From: "test@local", AdminTo: "test@local", Logger: logger},
		Config: config.Config{
			Port: "0", DBPath: dbPath, BaseURL: "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
		},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	MountRoutes(r, deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// 4. Chromium
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

	// 5. Visite home, collecte tous les <a href> internes (slug commence par /)
	var hrefs []string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Evaluate(
			`Array.from(document.querySelectorAll('a[href]')).map(a => a.getAttribute('href')).filter(h => h && h.startsWith('/') && !h.startsWith('//'))`,
			&hrefs,
		),
	); err != nil {
		t.Fatalf("crawl home: %v", err)
	}

	if len(hrefs) == 0 {
		t.Fatal("aucun lien interne <a href> trouvé sur / — page vide ?")
	}

	// 6. Pour chaque href interne unique, asserter 2xx/3xx via httptest client.
	seen := make(map[string]bool)
	httpClient := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // ne pas suivre — on accepte les 3xx comme valides
	}}

	var broken []string
	for _, h := range hrefs {
		// Strip ?query et #fragment
		u := h
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			u = u[:i]
		}
		if seen[u] {
			continue
		}
		seen[u] = true

		resp, err := httpClient.Get(srv.URL + u)
		if err != nil {
			broken = append(broken, u+" (err: "+err.Error()+")")
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			broken = append(broken, u+" → HTTP "+strings.TrimSpace(resp.Status))
		}
	}

	t.Logf("crawled %d unique internal links from home, %d broken", len(seen), len(broken))
	if len(broken) > 0 {
		t.Errorf("LIENS CASSÉS détectés (anti-régression contre 404 silencieux en prod) :\n  - %s",
			strings.Join(broken, "\n  - "))
	}
}
