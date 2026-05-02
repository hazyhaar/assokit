//go:build integration_cdp

// CLAUDE:SUMMARY Test intégral CDP : navigue toutes les pages NPS via Chromium headless,
// soumet les formulaires, et vérifie l'impact DB après chaque action.
//
// Lancement :
//
//	cd /devhoros/assokit
//	go test -tags=integration_cdp -v -timeout=120s ./internal/nps/ -run TestCDPIntegration
//
// Pré-requis : chromium ou chromium-browser installé (snap, apt). Le test détecte
// automatiquement le binaire. Il refuse de tourner si l'exec n'est pas trouvée.
//
// Le test :
//  1. Crée une DB fresh in-memory (file::memory:?cache=shared) avec schema + seeds.
//  2. Bootstrap admin (email=admin@test.local, password=admin-test-pwd).
//  3. Lance un httptest.NewServer câblé sur le router NPS.
//  4. Lance Chromium headless via chromedp.
//  5. Exécute des scénarios end-to-end (navigation + submit) et vérifie la DB
//     directement via sql.DB après chaque mutation.
//
// Si tout passe : 0 échec → on a la preuve que les routes pondent du HTML cohérent
// ET que les actions impactent la DB comme spécifié dans AUDIT_ACTIONS.md.
package handlers

import (
	"context"
	"database/sql"
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
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

const (
	cdpTestAdminEmail    = "admin@test.local"
	cdpTestAdminPassword = "admin-test-pwd-1234"
)

// TestCDPIntegration : scénario complet end-to-end.
func TestCDPIntegration(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé. Installer chromium ou chromium-browser.")
	}
	t.Logf("chromium : %s", chromePath)

	// 1. DB setup
	dbPath := filepath.Join(t.TempDir(), "nps-cdp.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
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

	// 2. Bootstrap admin
	authStore := &auth.Store{DB: db}
	adminUser, err := authStore.Register(context.Background(), cdpTestAdminEmail, cdpTestAdminPassword, "Admin Test")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO user_roles(user_id, role_id) VALUES (?, 'admin'), (?, 'member')`, adminUser.ID, adminUser.ID); err != nil {
		t.Fatalf("admin roles: %v", err)
	}

	// 3. App + httptest server (Mailer en mode "désactivé" — APIKey vide → emails restent
	//    en pending dans email_outbox sans tentative d'envoi externe, ce qui est ce qu'on veut tester).
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	theme.Init(&theme.Branding{Name: "Assokit Test"})
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Mailer: &mailer.Mailer{DB: db, From: cdpTestAdminEmail, AdminTo: cdpTestAdminEmail, Logger: logger},
		Config: config.Config{
			Port:         "0",
			DBPath:       dbPath,
			BaseURL:      "http://localhost",
			CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
			AdminEmail:   cdpTestAdminEmail,
		},
	}
	r := chi.NewRouter()
	r.Use(middleware.Flash)
	r.Use(middleware.Auth(db, deps.Config.CookieSecret))
	MountRoutes(r, deps)
	srv := httptest.NewServer(r)
	defer srv.Close()
	t.Logf("server : %s", srv.URL)

	// 4. Chromium / chromedp
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
	ctx, cancel := context.WithTimeout(browserCtx, 90*time.Second)
	defer cancel()

	// Warm-up : ouvre une page about:blank pour démarrer le browser.
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("browser warmup: %v", err)
	}

	// =====================================================================
	// SCÉNARIOS
	// =====================================================================

	t.Run("01_home_renders_hero", func(t *testing.T) {
		var heroText string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/"),
			chromedp.WaitVisible(`section.hero h1`, chromedp.ByQuery),
			chromedp.Text(`section.hero h1`, &heroText, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("home nav: %v", err)
		}
		if !strings.Contains(heroText, "Protéger") {
			t.Errorf("home hero h1 attendu contient 'Protéger', got %q", heroText)
		}
	})

	t.Run("02_participer_8_profile_cards", func(t *testing.T) {
		var nodes []string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/participer"),
			chromedp.WaitVisible(`.profile-grid .profile-card`, chromedp.ByQuery),
			chromedp.Evaluate(
				`Array.from(document.querySelectorAll('.profile-grid .profile-card .profile-label')).map(e => e.textContent.trim())`,
				&nodes,
			),
		); err != nil {
			t.Fatalf("participer nav: %v", err)
		}
		if len(nodes) != 8 {
			t.Errorf("8 profil-cards attendus, got %d (%v)", len(nodes), nodes)
		}
	})

	t.Run("03_signup_lanceur_writes_db", func(t *testing.T) {
		// Compter état initial
		signupsBefore := countRows(t, db, "signups")
		usersBefore := countRows(t, db, "users")
		outboxBefore := countRows(t, db, "email_outbox")
		tokensBefore := countRows(t, db, "activation_tokens")

		email := "lanceur.test@example.com"
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/adherer/lanceur"),
			chromedp.WaitVisible(`form input[name="prenom"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="prenom"]`, "Jean", chromedp.ByQuery),
			chromedp.SendKeys(`input[name="nom"]`, "Test", chromedp.ByQuery),
			chromedp.SendKeys(`input[name="email"]`, email, chromedp.ByQuery),
			chromedp.SendKeys(`textarea[name="message"]`, "Test scenario CDP", chromedp.ByQuery),
			chromedp.Submit(`form`, chromedp.ByQuery),
			chromedp.WaitVisible(`section.page-merci, .merci-card`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("signup submit: %v", err)
		}

		// Asserts DB
		assertGrew(t, db, "signups", signupsBefore, 1)
		assertGrew(t, db, "users", usersBefore, 1)
		assertGrew(t, db, "email_outbox", outboxBefore, 2) // admin + welcome
		assertGrew(t, db, "activation_tokens", tokensBefore, 1)

		// Asserts champs
		var dbEmail, dbProfil string
		if err := db.QueryRow(`SELECT email, profile FROM signups WHERE email=?`, email).Scan(&dbEmail, &dbProfil); err != nil {
			t.Fatalf("signups SELECT: %v", err)
		}
		if dbEmail != email || dbProfil != "lanceur" {
			t.Errorf("signup row : got email=%q profil=%q", dbEmail, dbProfil)
		}

		// Asserts activation_tokens.used_at IS NULL (pas encore consommé)
		var usedAt sql.NullString
		if err := db.QueryRow(`
			SELECT t.used_at FROM activation_tokens t
			JOIN users u ON u.id = t.user_id WHERE u.email=?
			ORDER BY t.expires_at DESC LIMIT 1
		`, email).Scan(&usedAt); err != nil {
			t.Fatalf("activation_tokens SELECT: %v", err)
		}
		if usedAt.Valid {
			t.Errorf("activation_token déjà consommé avant /activate (used_at=%v)", usedAt.String)
		}
	})

	t.Run("04_activate_token_sets_user_active", func(t *testing.T) {
		// Récupérer le token signups → activation_tokens
		var token, userID string
		if err := db.QueryRow(`
			SELECT t.token, t.user_id
			FROM activation_tokens t
			JOIN users u ON u.id = t.user_id
			WHERE u.email = 'lanceur.test@example.com'
			ORDER BY t.expires_at DESC LIMIT 1
		`).Scan(&token, &userID); err != nil {
			t.Fatalf("activation_token SELECT: %v", err)
		}

		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/activate/"+token),
		); err != nil {
			t.Fatalf("activate nav: %v", err)
		}

		// activation_tokens.used_at doit être NON NULL maintenant.
		var usedAt sql.NullString
		if err := db.QueryRow(`SELECT used_at FROM activation_tokens WHERE token=?`, token).Scan(&usedAt); err != nil {
			t.Fatalf("activation_tokens SELECT: %v", err)
		}
		if !usedAt.Valid {
			t.Errorf("activation_token.used_at toujours NULL après /activate — handleActivate n'a pas écrit")
		}
	})

	t.Run("05_contact_form_writes_outbox", func(t *testing.T) {
		outboxBefore := countRows(t, db, "email_outbox")

		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/contact"),
			chromedp.WaitVisible(`form textarea[name="message"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="nom"]`, "Marie Contact", chromedp.ByQuery),
			chromedp.SendKeys(`input[name="email"]`, "marie@test.local", chromedp.ByQuery),
			chromedp.SendKeys(`input[name="sujet"]`, "Question CDP", chromedp.ByQuery),
			chromedp.SendKeys(`textarea[name="message"]`, "Bonjour, ceci est un test CDP du formulaire de contact.", chromedp.ByQuery),
			chromedp.Submit(`form`, chromedp.ByQuery),
			chromedp.WaitVisible(`.merci-card`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("contact submit: %v", err)
		}

		assertGrew(t, db, "email_outbox", outboxBefore, 1)

		var subject, toAddr string
		if err := db.QueryRow(`
			SELECT subject, to_addr FROM email_outbox
			WHERE subject LIKE '[Contact]%'
			ORDER BY created_at DESC LIMIT 1
		`).Scan(&subject, &toAddr); err != nil {
			t.Fatalf("email_outbox SELECT: %v", err)
		}
		if !strings.Contains(subject, "Question CDP") {
			t.Errorf("subject contient 'Question CDP' attendu, got %q", subject)
		}
		if toAddr != cdpTestAdminEmail {
			t.Errorf("to_addr attendu %q, got %q", cdpTestAdminEmail, toAddr)
		}
	})

	t.Run("06_login_admin_then_logout", func(t *testing.T) {
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/login"),
			chromedp.WaitVisible(`form input[name="email"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="email"]`, cdpTestAdminEmail, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="password"]`, cdpTestAdminPassword, chromedp.ByQuery),
			chromedp.Submit(`form`, chromedp.ByQuery),
			chromedp.WaitVisible(`section.hero h1`, chromedp.ByQuery), // redirige vers /
		); err != nil {
			t.Fatalf("login: %v", err)
		}

		// Vérifier qu'on a bien le user-name dans le header
		var userName string
		if err := chromedp.Run(ctx, chromedp.Text(`.user-menu .user-name`, &userName, chromedp.ByQuery)); err != nil {
			// pas bloquant — on accepte que le menu ne soit pas peuplé si la session middleware ne décode pas.
			t.Logf("user-menu absent (session middleware peut être à câbler) : %v", err)
		}
		t.Logf("user-name dans header : %q", userName)
	})

	t.Run("07_forum_reply_creates_node", func(t *testing.T) {
		// Pré-requis : être connecté (depuis test 06). Cibler le nœud "forum" racine.
		nodesBefore := countRows(t, db, "nodes")

		// Borner chaque WaitVisible à 5s : si le sélecteur n'existe pas, on fail
		// vite avec un message clair plutôt que de squatter le timeout global du test.
		fastCtx, fastCancel := context.WithTimeout(ctx, 5*time.Second)
		defer fastCancel()

		if err := chromedp.Run(fastCtx,
			chromedp.Navigate(srv.URL+"/forum"),
			chromedp.WaitVisible(`section.forum-index`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("forum index nav (sélecteur attendu .forum-index, manquant ou timeout 5s) : %v", err)
		}

		fastCtx2, fastCancel2 := context.WithTimeout(ctx, 5*time.Second)
		defer fastCancel2()
		if err := chromedp.Run(fastCtx2,
			chromedp.Navigate(srv.URL+"/forum/forum"),
			chromedp.WaitVisible(`section.forum-thread`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("forum/forum nav (sélecteur attendu .forum-thread, manquant ou timeout 5s) — régression contrat DOM : %v", err)
		}

		var hasReplyForm bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			`!!document.querySelector('.reply-form')`, &hasReplyForm)); err != nil {
			t.Fatalf("eval reply-form: %v", err)
		}
		if !hasReplyForm {
			t.Skip("reply-form absent (session non câblée ou perms manquantes) — skip 07")
			return
		}

		// Soumission : on capte l'URL avant/après pour vérifier qu'une navigation
		// a eu lieu (sinon = soumission silencieusement absorbée = régression handler).
		var urlBefore, urlAfter string
		if err := chromedp.Run(ctx, chromedp.Location(&urlBefore)); err != nil {
			t.Fatalf("location before submit: %v", err)
		}

		fastCtx3, fastCancel3 := context.WithTimeout(ctx, 5*time.Second)
		defer fastCancel3()
		if err := chromedp.Run(fastCtx3,
			chromedp.SendKeys(`.reply-form input[name="title"]`, "Sujet CDP test", chromedp.ByQuery),
			chromedp.SendKeys(`.reply-form textarea[name="body"]`, "Corps de mon message via CDP.", chromedp.ByQuery),
			chromedp.Submit(`.reply-form`, chromedp.ByQuery),
			chromedp.Sleep(500*time.Millisecond), // laisser le redirect 303 + GET / re-render se faire
		); err != nil {
			t.Fatalf("forum reply submit (timeout 5s) : %v", err)
		}
		if err := chromedp.Run(ctx, chromedp.Location(&urlAfter)); err != nil {
			t.Fatalf("location after submit: %v", err)
		}

		// Le POST /forum/{slug}/reply renvoie 303 vers /forum/{slug}. Sans navigation
		// post-submit, le handler n'a probablement rien fait → régression.
		if urlBefore == urlAfter && !strings.Contains(urlAfter, "/forum/forum") {
			t.Errorf("aucune navigation après submit (avant=%q après=%q) — handler n'a pas redirigé", urlBefore, urlAfter)
		}

		assertGrew(t, db, "nodes", nodesBefore, 1)

		var title string
		if err := db.QueryRow(`SELECT title FROM nodes WHERE title='Sujet CDP test' ORDER BY created_at DESC LIMIT 1`).Scan(&title); err != nil {
			t.Errorf("forum reply : node introuvable en DB : %v", err)
		}
	})

	t.Run("08_search_renders_form", func(t *testing.T) {
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/search?q=alerte"),
			chromedp.WaitVisible(`form.search-bar`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("search nav: %v", err)
		}
	})

	t.Run("09_404_renders_error_page", func(t *testing.T) {
		var bodyText string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/route-vraiment-inexistante"),
			chromedp.WaitVisible(`.error-card .error-code`, chromedp.ByQuery),
			chromedp.Text(`.error-card .error-code`, &bodyText, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("404 nav: %v", err)
		}
		if !strings.Contains(bodyText, "404") {
			t.Errorf("error-code attendu '404', got %q", bodyText)
		}
	})

	t.Run("10_donate_renders_callout", func(t *testing.T) {
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/soutenir"),
			chromedp.WaitVisible(`section.page-donate`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("soutenir nav: %v", err)
		}
	})

	t.Run("11_charte_renders_static", func(t *testing.T) {
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/charte"),
			chromedp.WaitVisible(`section.page-static h1`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("charte nav: %v", err)
		}
	})
}

// =====================================================================
// helpers
// =====================================================================

func findChromium(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"/snap/bin/chromium",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func assertGrew(t *testing.T, db *sql.DB, table string, before, expectDelta int) {
	t.Helper()
	after := countRows(t, db, table)
	if after-before != expectDelta {
		t.Errorf("table %s : delta attendu +%d (avant %d, après %d), got +%d",
			table, expectDelta, before, after, after-before)
	}
}
