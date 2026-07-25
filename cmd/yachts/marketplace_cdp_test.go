//go:build integration_cdp

// CLAUDE:SUMMARY Test E2E CDP du parcours marketplace yachts : grille -> facette marque
// -> filtre range longueur -> page détail -> widget feedback (présence en CDP, soumission
// via client HTTP authentifié CSRF, lecture via GET /feedback/comments).
//
// Lancement (Chrome for Testing fourni par CHROME_PATH) :
//
//	cd /devhoros/assokit
//	CHROME_PATH=/devhoros/horos55/vendor/chrome/chrome-linux64/chrome \
//	  CGO_ENABLED=1 go test -tags=integration_cdp -run TestMarketplaceCDP ./cmd/yachts/...
//
// Le test monte l'application core complète via api.New (mêmes middlewares CSRF/Auth que
// la prod) avec le hook RegisterRoutes de l'instance yachts, sur un httptest.Server, puis
// pilote Chrome for Testing via chromedp. Données = seed réel A&C (seedIfEmpty), aucun mock.
//
// Skip propre si aucun binaire Chrome (CHROME_PATH / ASSOKIT_CHROME / candidats système) —
// même politique que internal/handlers/integration_cdp_test.go.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/hazyhaar/assokit/pkg/api"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/listing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func findChrome(t *testing.T) string {
	t.Helper()
	for _, env := range []string{"CHROME_PATH", "ASSOKIT_CHROME"} {
		if p := os.Getenv(env); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, c := range []string{
		"/snap/bin/chromium",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// TestMarketplaceCDP : parcours marketplace bout-en-bout sur l'instance yachts.
func TestMarketplaceCDP(t *testing.T) {
	chromePath := findChrome(t)
	if chromePath == "" {
		t.Skip("aucun binaire Chrome trouvé (CHROME_PATH/ASSOKIT_CHROME/candidats système) — test skippé")
	}
	t.Logf("chrome : %s", chromePath)

	// 1. App core complète (api.New) + hook routes yachts, sur DB temp dédiée.
	silo, err := listing.LoadSilo("../../config/silos/yachts.toml")
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	tmp := t.TempDir()
	siloDBPath := filepath.Join(tmp, "yachts.db")
	appDBPath := filepath.Join(tmp, "assokit.db")

	siloDB, err := sql.Open("sqlite", siloDBPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open silo db: %v", err)
	}
	t.Cleanup(func() { siloDB.Close() })

	store, err := listing.NewStore(context.Background(), siloDB, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := seedIfEmpty(context.Background(), store); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := &yachtsHandlers{store: store, silo: silo}

	// Handle RO sur la DB core (table feedbacks) — comme cmd/yachts/main.go ouvre coreDB
	// distinct du handle interne d'api.New. Le fichier est créé par api.New (migrations).
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	var fbDB *sql.DB
	app, err := api.New(api.Options{
		Port:         "0",
		DBPath:       appDBPath,
		CookieSecret: []byte("test-cookie-secret-32bytes-padded000"),
		AdminEmail:   "admin@test.local",
		Logger:       logger,
		RegisterRoutes: func(r chi.Router) error {
			r.Get("/annonces", h.handleIndex)
			r.Get("/annonces/fragment", h.handleFragment)
			r.Get("/annonces/{id}", h.handleDetail)
			// Le fichier core existe désormais (migrations appliquées) : on ouvre un
			// second handle pour le handler de lecture, comme la bordure de prod.
			db, oerr := sql.Open("sqlite", appDBPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
			if oerr != nil {
				return oerr
			}
			fbDB = db
			fbh := &feedbackCommentsHandler{db: db, appName: silo.ID, logger: logger}
			r.Get("/feedback/comments", fbh.ServeHTTP)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	t.Cleanup(func() {
		if fbDB != nil {
			fbDB.Close()
		}
	})

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	t.Logf("server : %s", srv.URL)

	// 2. Chrome / chromedp.
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

	// (a) grille rend ≥1 carte, au moins un modèle réel visible.
	t.Run("a_grille_rend_cartes_reelles", func(t *testing.T) {
		step, stepCancel := context.WithTimeout(ctx, 15*time.Second)
		defer stepCancel()
		var titles []string
		if err := chromedp.Run(step,
			chromedp.Navigate(srv.URL+"/annonces"),
			chromedp.WaitVisible(`#listing-results [data-testid="listing-card"]`, chromedp.ByQuery),
			chromedp.Evaluate(
				`Array.from(document.querySelectorAll('#listing-results [data-testid="listing-card"] h3')).map(e => e.textContent.trim())`,
				&titles),
		); err != nil {
			t.Fatalf("nav /annonces: %v", err)
		}
		if len(titles) == 0 {
			t.Fatalf("aucune carte rendue dans #listing-results")
		}
		joined := strings.Join(titles, " | ")
		if !strings.Contains(joined, "NEEL 43") && !strings.Contains(joined, "Sun Odyssey 440") {
			t.Fatalf("aucun modèle réel attendu (NEEL 43 / Sun Odyssey 440) dans : %q", joined)
		}
		t.Logf("cartes rendues (%d) : %s", len(titles), joined)
	})

	// (b) facette marque=Jeanneau via la sidebar → fragment ne contient QUE des Jeanneau.
	t.Run("b_facette_marque_jeanneau", func(t *testing.T) {
		step, stepCancel := context.WithTimeout(ctx, 15*time.Second)
		defer stepCancel()
		var marques []string
		if err := chromedp.Run(step,
			chromedp.Navigate(srv.URL+"/annonces"),
			chromedp.WaitVisible(`form select[name="marque"]`, chromedp.ByQuery),
			chromedp.SetValue(`form select[name="marque"]`, "Jeanneau", chromedp.ByQuery),
			chromedp.Click(`form button[type="submit"]`, chromedp.ByQuery),
			// attendre que le fragment re-rendu ne montre plus que des cartes Jeanneau
			chromedp.WaitVisible(`#listing-results [data-testid="listing-card"][data-marque="Jeanneau"]`, chromedp.ByQuery),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(
				`Array.from(document.querySelectorAll('#listing-results [data-testid="listing-card"]')).map(e => e.getAttribute('data-marque'))`,
				&marques),
		); err != nil {
			t.Fatalf("facette marque: %v", err)
		}
		if len(marques) == 0 {
			t.Fatalf("aucune carte après filtre marque=Jeanneau")
		}
		for _, m := range marques {
			if m != "Jeanneau" {
				t.Fatalf("le fragment marque=Jeanneau laisse passer une carte marque=%q (set: %v)", m, marques)
			}
		}
		t.Logf("filtre marque=Jeanneau : %d cartes, toutes Jeanneau", len(marques))
	})

	// (c) filtre range longueur (borne min) → résultats cohérents avec la borne.
	t.Run("c_range_longueur_min", func(t *testing.T) {
		step, stepCancel := context.WithTimeout(ctx, 15*time.Second)
		defer stepCancel()
		const minLen = 14.0
		var lengths []float64
		if err := chromedp.Run(step,
			chromedp.Navigate(srv.URL+"/annonces"),
			chromedp.WaitVisible(`form input[name="longueur_m_min"]`, chromedp.ByQuery),
			chromedp.SetValue(`form input[name="longueur_m_min"]`, "14", chromedp.ByQuery),
			chromedp.Click(`form button[type="submit"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#listing-results`, chromedp.ByQuery),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.Evaluate(
				`Array.from(document.querySelectorAll('#listing-results [data-testid="listing-card"]')).map(e => parseFloat(e.getAttribute('data-longueur')))`,
				&lengths),
		); err != nil {
			t.Fatalf("range longueur: %v", err)
		}
		if len(lengths) == 0 {
			t.Fatalf("aucune carte avec longueur >= %.0f m alors que le seed en contient", minLen)
		}
		for _, l := range lengths {
			if l < minLen {
				t.Fatalf("carte de longueur %.2f m < borne min %.0f m", l, minLen)
			}
		}
		t.Logf("filtre longueur>=%.0f : %d cartes, longueurs %v", minLen, len(lengths), lengths)
	})

	// (d) page détail : specs (longueur, année), description, vendeur, cadre "Photo à venir".
	t.Run("d_page_detail", func(t *testing.T) {
		all, err := store.Search(context.Background(), listing.Filter{Limit: 100})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var target listing.Listing
		for _, l := range all {
			if l.Title == "NEEL 43" {
				target = l
				break
			}
		}
		if target.ID == "" {
			t.Fatalf("annonce NEEL 43 absente du seed")
		}

		step, stepCancel := context.WithTimeout(ctx, 15*time.Second)
		defer stepCancel()
		var body, photoText string
		if err := chromedp.Run(step,
			chromedp.Navigate(srv.URL+"/annonces/"+target.ID),
			chromedp.WaitVisible(`[data-testid="detail-page"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="detail-page"]`, &body, chromedp.ByQuery),
			chromedp.Text(`[data-testid="detail-page"]`, &photoText, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav détail: %v", err)
		}
		for _, want := range []string{"Caractéristiques", "Longueur", "Année", "Description", "Vendeur", "A&C Yacht Brokers"} {
			if !strings.Contains(body, want) {
				t.Fatalf("page détail ne contient pas %q\n---\n%s", want, body)
			}
		}
		if !strings.Contains(photoText, "Photo à venir") {
			t.Fatalf("cadre photo-absente attendu (\"Photo à venir\"), non trouvé dans la page détail")
		}
		t.Logf("détail NEEL 43 : specs+description+vendeur OK, cadre \"Photo à venir\" présent")
	})

	// (e) FEEDBACK : le widget feedback n'est rendu QUE pour un utilisateur identifié
	//     (shell.templ : `if u := UserFromContext(ctx); u != nil`). Un navigateur CDP
	//     anonyme ne le voit donc jamais — et le POST /feedback est lui-même réservé aux
	//     identifiés + protégé CSRF (double-submit cookie). On exécute donc cette étape
	//     via un http.Client authentifié (cookie jar) qui : (1) se loggue (CSRF), (2)
	//     charge /annonces et asserte la PRÉSENCE du widget dans le HTML servi à l'usager
	//     connecté, (3) soumet le feedback, (4) le relit via GET /feedback/comments (le
	//     contrat consommé par le worker feedback-pull horos55). Aucun mock.
	t.Run("e_feedback_present_submit_read", func(t *testing.T) {
		authStore := &identity.Store{DB: fbDB}
		if _, err := authStore.Register(context.Background(), "tester@test.local", "feedback-pwd-12345", "Tester"); err != nil {
			t.Fatalf("register tester: %v", err)
		}

		jar, _ := cookiejar.New(nil)
		client := &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // on inspecte les 303 sans les suivre
			},
		}
		u, _ := url.Parse(srv.URL)
		readCSRF := func() string {
			for _, c := range jar.Cookies(u) {
				if c.Name == "assokit_csrf" {
					return c.Value
				}
			}
			return ""
		}

		// GET /login pour amorcer le cookie CSRF, puis POST /login avec _csrf.
		if r, err := client.Get(srv.URL + "/login"); err != nil {
			t.Fatalf("GET /login: %v", err)
		} else {
			r.Body.Close()
		}
		loginCSRF := readCSRF()
		if loginCSRF == "" {
			t.Fatalf("cookie CSRF absent après GET /login")
		}
		loginResp, err := client.PostForm(srv.URL+"/login", url.Values{
			"_csrf":    {loginCSRF},
			"email":    {"tester@test.local"},
			"password": {"feedback-pwd-12345"},
		})
		if err != nil {
			t.Fatalf("POST /login: %v", err)
		}
		loginResp.Body.Close()
		if loginResp.StatusCode != http.StatusSeeOther {
			t.Fatalf("login: status %d attendu 303", loginResp.StatusCode)
		}

		// (présence widget) GET /annonces en tant qu'usager connecté → le widget feedback
		// (bouton hx-get vers /feedback/form) doit être dans le HTML.
		pageURL := srv.URL + "/annonces"
		pageResp, err := client.Get(pageURL)
		if err != nil {
			t.Fatalf("GET /annonces (authentifié): %v", err)
		}
		pageHTML, _ := io.ReadAll(pageResp.Body)
		pageResp.Body.Close()
		if !strings.Contains(string(pageHTML), "feedback-fab-container") ||
			!strings.Contains(string(pageHTML), "/feedback/form") {
			t.Fatalf("widget feedback absent du HTML servi à l'usager connecté")
		}
		t.Logf("widget feedback présent dans la page de l'usager connecté")

		// GET /feedback/form → pose/expose le cookie CSRF (double-submit).
		formResp, err := client.Get(srv.URL + "/feedback/form?url=" + url.QueryEscape(pageURL) + "&title=" + url.QueryEscape("Annonces yachts"))
		if err != nil {
			t.Fatalf("GET /feedback/form: %v", err)
		}
		formResp.Body.Close()
		if formResp.StatusCode != http.StatusOK {
			t.Fatalf("GET /feedback/form: status %d", formResp.StatusCode)
		}

		csrf := readCSRF()
		if csrf == "" {
			t.Fatalf("cookie CSRF assokit_csrf absent après GET /feedback/form")
		}

		const msg = "Test E2E CDP : le parcours facettes vers détail est fluide sur cette page."
		postResp, err := client.PostForm(srv.URL+"/feedback", url.Values{
			"_csrf":      {csrf},
			"message":    {msg},
			"page_url":   {pageURL},
			"page_title": {"Annonces yachts"},
		})
		if err != nil {
			t.Fatalf("POST /feedback: %v", err)
		}
		postResp.Body.Close()
		if postResp.StatusCode != http.StatusOK {
			t.Fatalf("POST /feedback: status %d attendu 200", postResp.StatusCode)
		}

		// GET /feedback/comments (contrat consommé par le hub feedback-pull) : le feedback
		// doit y apparaître. Monté directement sur le DB core de l'app.
		commentsResp, err := client.Get(srv.URL + "/feedback/comments")
		if err != nil {
			t.Fatalf("GET /feedback/comments: %v", err)
		}
		defer commentsResp.Body.Close()
		if commentsResp.StatusCode != http.StatusOK {
			t.Fatalf("GET /feedback/comments: status %d", commentsResp.StatusCode)
		}
		var comments []remoteComment
		if err := json.NewDecoder(commentsResp.Body).Decode(&comments); err != nil {
			t.Fatalf("decode /feedback/comments: %v", err)
		}
		found := false
		for _, c := range comments {
			if c.Text == msg {
				found = true
				if c.PageURL != pageURL {
					t.Fatalf("feedback page_url=%q attendu %q", c.PageURL, pageURL)
				}
			}
		}
		if !found {
			t.Fatalf("feedback soumis introuvable dans GET /feedback/comments (%d commentaires)", len(comments))
		}
		t.Logf("feedback soumis (CSRF double-submit, user identifié) et relu via /feedback/comments OK")
	})
}
