//go:build integration_cdp

// CLAUDE:SUMMARY Test CDP FAQ/lexique : une page CMS gravée au slug `faq` se rend
// publiquement à /faq via le routage de pages existant (handlePage), et le lien de
// pied de page pointe bien vers /faq. Aucun module faq dédié : réutilisation pure du
// CMS (tree.Store + action pages.create/update + handlePage).
//
// Lancement :
//
//	cd /devhoros/assokit
//	go test -tags=integration_cdp -v -timeout=120s ./internal/handlers/ -run TestCDPFAQ
package handlers

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"log/slog"

	tree "github.com/hazyhaar/nodetree"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

// TestCDPFAQ prouve qu'une page éditée au slug `faq` (via le même tree.Store que
// l'action admin pages.create) se rend publiquement à /faq, et que le pied de page
// expose le lien.
func TestCDPFAQ(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé.")
	}

	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema run: %v", err)
	}
	seedRoles(t, db)
	if err := SeedNodes(db); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// Édition admin simulée : grave la page `faq` exactement comme le fait
	// l'action pages.create (même tree.Store, type "page"). Aucun contenu mock
	// métier figé n'est committé — ce corps n'existe que le temps du test.
	store := &tree.Store{DB: db}
	const faqBodyMD = "Cette FAQ est éditée par l'administrateur."
	if _, err := store.Create(context.Background(), tree.Node{
		Slug:   "faq",
		Type:   "page",
		Title:  "Foire aux questions",
		BodyMD: faqBodyMD,
	}); err != nil {
		t.Fatalf("create faq page: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{
			Port:         "0",
			BaseURL:      "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
		},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	if err := MountRoutes(r, deps); err != nil {
		t.Fatalf("MountRoutes: %v", err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("browser warmup: %v", err)
	}

	t.Run("faq_page_renders_admin_body", func(t *testing.T) {
		var h1, body string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/faq"),
			chromedp.WaitVisible(`section.page-static h1`, chromedp.ByQuery),
			chromedp.Text(`section.page-static h1`, &h1, chromedp.ByQuery),
			chromedp.Text(`section.page-static`, &body, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("faq nav: %v", err)
		}
		if !strings.Contains(h1, "Foire aux questions") {
			t.Errorf("titre /faq attendu 'Foire aux questions', got %q", h1)
		}
		if !strings.Contains(body, "éditée par l'administrateur") {
			t.Errorf("corps /faq ne rend pas le contenu admin gravé, got %q", body)
		}
	})

	t.Run("lexique_empty_state_clean", func(t *testing.T) {
		// Le slug `lexique` n'a pas été gravé : le routage doit rendre un état
		// vide propre (titre + corps vide), jamais une erreur brute.
		var h1 string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/lexique"),
			chromedp.WaitVisible(`section.page-static h1`, chromedp.ByQuery),
			chromedp.Text(`section.page-static h1`, &h1, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("lexique nav: %v", err)
		}
		if !strings.Contains(h1, "Lexique") {
			t.Errorf("titre /lexique attendu 'Lexique', got %q", h1)
		}
	})

	t.Run("footer_links_faq_and_lexique", func(t *testing.T) {
		var hrefs []string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/"),
			chromedp.WaitVisible(`footer`, chromedp.ByQuery),
			chromedp.Evaluate(
				`Array.from(document.querySelectorAll('footer a')).map(a => a.getAttribute('href'))`,
				&hrefs,
			),
		); err != nil {
			t.Fatalf("footer nav: %v", err)
		}
		joined := strings.Join(hrefs, " ")
		if !strings.Contains(joined, "/faq") || !strings.Contains(joined, "/lexique") {
			t.Errorf("pied de page sans lien /faq et /lexique, got %v", hrefs)
		}
	})
}
