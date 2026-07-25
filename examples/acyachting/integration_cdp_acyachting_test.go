//go:build integration_cdp

// Gate E2E de l'instance ACyachting (réplication locale, non déployée).
//
// Ce test prouve, via Chromium headless (chromedp), le chemin RÉEL de
// provisionnement d'une instance assokit répliquée :
//
//  1. l'instance démarre par la façade de bordure api.New, avec le branding
//     ACyachting (examples/acyachting/config/branding.toml) injecté en Option ;
//  2. la page d'accueil rend la charte brandée (le nom « ACyachting » dans le
//     hero, la tagline nautique dans le h1) ;
//  3. le compte de Dominique, créé par la voie native de bootstrap
//     (api.Options.AdminEmail/AdminPassword → bootstrap.BootstrapAdmin, bcrypt,
//     grade sys-admin), se connecte par le formulaire /login natif.
//
// Lancement :
//
//	cd /devhoros/assokit
//	CGO_ENABLED=0 go test -tags=integration_cdp -v -timeout=120s \
//	  ./examples/acyachting/ -run TestACyachtingInstanceE2E
//
// Pré-requis : un binaire Chromium (détecté automatiquement, ou CHROME_PATH).
// Le test n'écrit que dans une DB temporaire (t.TempDir) — il ne touche pas la
// DB de l'instance de run, et ne déploie rien.
package acyachting_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/hazyhaar/assokit/pkg/api"
)

// Identité d'instance de Dominique. L'email tient lieu d'identifiant de login.
// Le mot de passe n'est PAS un secret de production : c'est une valeur de test
// locale, injectée à la bordure du test exactement comme run-local.sh injecte
// ACYACHTING_ADMIN_PASSWORD à la bordure de l'instance réelle.
const (
	dominiqueEmail    = "dominique@acyachting.local"
	dominiquePassword = "acyachting-e2e-pwd-7421"
	brandName         = "ACyachting"
)

func TestACyachtingInstanceE2E(t *testing.T) {
	chromePath := findChromium(t)
	if chromePath == "" {
		t.Skip("aucun binaire chromium trouvé — test skippé. Installer chromium ou définir CHROME_PATH.")
	}
	t.Logf("chromium : %s", chromePath)

	// Branding ACyachting : on charge le MÊME branding.toml que l'instance de run,
	// pour que le gate prouve la charte réellement servie (pas une copie de test).
	brandingDir, err := filepath.Abs("config")
	if err != nil {
		t.Fatalf("abs config dir: %v", err)
	}

	// L'instance démarre par la façade de bordure, exactement comme cmd/assokit :
	// branding injecté en Option, compte de Dominique bootstrappé par api.New.
	app, err := api.New(api.Options{
		DBPath:        filepath.Join(t.TempDir(), "acyachting-e2e.db"),
		Port:          "0",
		BaseURL:       "http://127.0.0.1",
		BrandingFS:    os.DirFS(brandingDir),
		AdminEmail:    dominiqueEmail,
		AdminPassword: dominiquePassword,
		CookieSecret:  []byte("acyachting-e2e-cookie-secret-32bytes!"),
	})
	if err != nil {
		t.Fatalf("api.New (provisionnement instance): %v", err)
	}

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	t.Logf("instance ACyachting : %s", srv.URL)

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

	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("browser warmup: %v", err)
	}

	t.Run("01_home_rend_la_charte_acyachting", func(t *testing.T) {
		var brandLabel, heroTitle, pageTitle string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/"),
			chromedp.WaitVisible(`section.hero h1`, chromedp.ByQuery),
			chromedp.Text(`section.hero .text-accent`, &brandLabel, chromedp.ByQuery),
			chromedp.Text(`section.hero h1`, &heroTitle, chromedp.ByQuery),
			chromedp.Title(&pageTitle),
		); err != nil {
			t.Fatalf("home nav: %v", err)
		}
		if !strings.Contains(brandLabel, brandName) {
			t.Errorf("nom d'instance dans le hero : attendu contient %q, got %q", brandName, brandLabel)
		}
		if !strings.Contains(pageTitle, brandName) {
			t.Errorf("title de page : attendu contient %q, got %q", brandName, pageTitle)
		}
		if strings.TrimSpace(heroTitle) == "" {
			t.Errorf("hero h1 vide — la tagline brandée ne rend pas")
		}
		t.Logf("hero brandé : label=%q title=%q h1=%q", brandLabel, pageTitle, heroTitle)
	})

	t.Run("02_dominique_se_connecte", func(t *testing.T) {
		var userName string
		if err := chromedp.Run(ctx,
			chromedp.Navigate(srv.URL+"/login"),
			chromedp.WaitVisible(`form input[name="email"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="email"]`, dominiqueEmail, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="password"]`, dominiquePassword, chromedp.ByQuery),
			chromedp.Submit(`form`, chromedp.ByQuery),
			chromedp.WaitVisible(`section.hero h1`, chromedp.ByQuery), // redirige vers /
			chromedp.Text(`.user-menu .user-name`, &userName, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("login de Dominique: %v", err)
		}
		if strings.TrimSpace(userName) == "" {
			t.Errorf("aucun user-name dans le header après login — la session de Dominique n'est pas établie")
		}
		t.Logf("Dominique connecté, header user-name=%q", userName)
	})

	t.Run("03_mauvais_mot_de_passe_refuse", func(t *testing.T) {
		// Garde négative : une identité erronée NE doit PAS établir de session.
		// On repart d'un contexte navigateur neuf (pas de cookie de session du 02).
		freshCtx, freshCancel := chromedp.NewContext(allocCtx)
		defer freshCancel()
		tctx, tcancel := context.WithTimeout(freshCtx, 30*time.Second)
		defer tcancel()

		var afterURL string
		if err := chromedp.Run(tctx,
			chromedp.Navigate(srv.URL+"/login"),
			chromedp.WaitVisible(`form input[name="email"]`, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="email"]`, dominiqueEmail, chromedp.ByQuery),
			chromedp.SendKeys(`input[name="password"]`, "mauvais-mot-de-passe", chromedp.ByQuery),
			chromedp.Submit(`form`, chromedp.ByQuery),
			chromedp.WaitVisible(`form input[name="email"]`, chromedp.ByQuery), // reste sur /login
			chromedp.Location(&afterURL),
		); err != nil {
			t.Fatalf("login échec attendu: %v", err)
		}
		if !strings.Contains(afterURL, "/login") {
			t.Errorf("un mauvais mot de passe ne devrait pas quitter /login, got %q", afterURL)
		}
	})
}

func findChromium(t *testing.T) string {
	t.Helper()
	for _, env := range []string{"CHROME_PATH", "ASSOKIT_CHROME"} {
		if p := os.Getenv(env); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, c := range []string{
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/snap/bin/chromium",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
