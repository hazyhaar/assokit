package api_test

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/api"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
)

// newTestApp crée une App minimale avec DB :memory: pour les tests.
func newTestApp(t *testing.T) *api.App {
	t.Helper()
	app, err := api.New(api.Options{
		DBPath:  ":memory:",
		BaseURL: "http://localhost",
		Port:    "0",
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return app
}

// TestAPI_NewServesSitemapXML vérifie que GET /sitemap.xml retourne 200
// avec Content-Type application/xml et un body XML valide.
func TestAPI_NewServesSitemapXML(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sitemap.xml")
	if err != nil {
		t.Fatalf("GET /sitemap.xml: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var v any
	if err := xml.Unmarshal(body, &v); err != nil {
		t.Errorf("body non parseable comme XML: %v\nbody=%s", err, body)
	}
}

// TestAPI_NewServesAllPublicRoutes vérifie que les routes publiques répondent
// sans retourner le placeholder "Bienvenue" du stub.
func TestAPI_NewServesAllPublicRoutes(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	// routes chi-routées : tout status 2xx-5xx est accepté (chi répond = OK).
	// Le critère clé est l'absence du placeholder stub "Bienvenue sur".
	// /forum et routes tree peuvent retourner 500 avec DB vide — normal.
	routes := []string{
		"/", "/forum", "/contact", "/soutenir", "/login",
		"/register", "/search", "/robots.txt", "/sitemap.xml",
	}

	for _, path := range routes {
		path := path
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()

			// Chi doit router (pas connection refused, pas 0) — tout 2xx..5xx est OK.
			if resp.StatusCode < 200 || resp.StatusCode > 599 {
				t.Errorf("%s: status inattendu = %d", path, resp.StatusCode)
			}

			// Critère clé : jamais le placeholder HTML stub.
			body, _ := io.ReadAll(resp.Body)
			bodyStr := string(body)
			if strings.Contains(bodyStr, "<h1>Bienvenue sur") {
				t.Errorf("%s: réponse contient le placeholder stub LOT1 '<h1>Bienvenue sur'", path)
			}
		})
	}
}

// TestSignupProfiles_DefaultRendersFourCardsNoEmoji vérifie que sans catalogue
// injecté, /participer rend les 4 profils génériques historiques, sans emoji.
func TestSignupProfiles_DefaultRendersFourCardsNoEmoji(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/participer")
	if err != nil {
		t.Fatalf("GET /participer: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	for _, label := range []string{"Adhérent", "Bénévole", "Donateur", "Partenaire"} {
		if !strings.Contains(html, label) {
			t.Errorf("/participer ne contient pas le profil %q", label)
		}
	}
	for _, emoji := range []string{"🙋", "💪", "💛", "🌐"} {
		if strings.Contains(html, emoji) {
			t.Errorf("/participer contient encore l'emoji %q (charte : aucun emoji)", emoji)
		}
	}
}

// TestSignupProfiles_CustomCatalogSeededGradeBoots vérifie qu'un catalogue
// injecté dont chaque grade existe dans la table RBAC démarre sans erreur.
func TestSignupProfiles_CustomCatalogSeededGradeBoots(t *testing.T) {
	t.Parallel()
	_, err := api.New(api.Options{
		DBPath:  ":memory:",
		BaseURL: "http://localhost",
		Port:    "0",
		SignupProfiles: []signupprofile.Profile{
			{ID: "moderateur", Label: "Modérateur", GradeID: "sys-moderator"},
		},
	})
	if err != nil {
		t.Fatalf("api.New avec grade seedé devrait réussir : %v", err)
	}
}

// TestSignupProfiles_UnknownGradeFailsBoot vérifie le fail-loud : un GradeID
// absent de la table RBAC fait échouer api.New, en nommant profil et grade.
func TestSignupProfiles_UnknownGradeFailsBoot(t *testing.T) {
	t.Parallel()
	_, err := api.New(api.Options{
		DBPath:  ":memory:",
		BaseURL: "http://localhost",
		Port:    "0",
		SignupProfiles: []signupprofile.Profile{
			{ID: "habitant", Label: "Habitant", GradeID: "grade-fantome"},
		},
	})
	if err == nil {
		t.Fatal("api.New devrait échouer sur un grade non seedé")
	}
	if !strings.Contains(err.Error(), "habitant") || !strings.Contains(err.Error(), "grade-fantome") {
		t.Errorf("message d'erreur doit nommer profil et grade : %v", err)
	}
}

// TestDisabledModules_ConventionsOffRouteNotMounted vérifie qu'avec le module
// « conventions » désactivé, la route d'espace membre /account/conventions n'est
// pas montée : le routeur répond 404 (route absente), indépendamment de l'auth —
// une instance verticale (ex. DEMOAPP) fournit alors sa propre vue à la même place.
func TestDisabledModules_ConventionsOffRouteNotMounted(t *testing.T) {
	t.Parallel()
	app, err := api.New(api.Options{
		DBPath:          ":memory:",
		BaseURL:         "http://localhost",
		Port:            "0",
		DisabledModules: []string{"conventions"},
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/account/conventions")
	if err != nil {
		t.Fatalf("GET /account/conventions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("module désactivé: status = %d, want 404 (route non montée)", resp.StatusCode)
	}
}

// TestDisabledModules_DefaultMountsConventions vérifie la rétro-compatibilité :
// sans DisabledModules, la route /account/conventions reste montée. La garde
// requireAuth de niveau route redirige alors un visiteur non authentifié vers le
// login (302) — jamais 404, ce qui prouverait que la route n'existe pas.
func TestDisabledModules_DefaultMountsConventions(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/account/conventions")
	if err != nil {
		t.Fatalf("GET /account/conventions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("défaut: status 404, la route devrait être montée (rétro-compat)")
	}
}

// TestDisabledModules_UnknownSlugFailsBoot vérifie le fail-loud : un slug de module
// inconnu du catalogue socle est fatal au New, jamais une désactivation silencieuse.
func TestDisabledModules_UnknownSlugFailsBoot(t *testing.T) {
	t.Parallel()
	_, err := api.New(api.Options{
		DBPath:          ":memory:",
		BaseURL:         "http://localhost",
		Port:            "0",
		DisabledModules: []string{"module-fantome"},
	})
	if err == nil {
		t.Fatal("api.New devrait échouer sur un slug de module inconnu")
	}
	if !strings.Contains(err.Error(), "module-fantome") {
		t.Errorf("message d'erreur doit nommer le slug fautif : %v", err)
	}
}

// TestProfileGrant_GovernanceNotRequestableFailsBoot vérifie l'invariant AM-O20 :
// le grade de gouvernance ne peut figurer parmi les grades requestables.
func TestProfileGrant_GovernanceNotRequestableFailsBoot(t *testing.T) {
	t.Parallel()
	_, err := api.New(api.Options{
		DBPath:  ":memory:",
		BaseURL: "http://localhost",
		Port:    "0",
		ProfileGrant: app.ProfileGrant{
			GovernanceGradeID:   "gov-grade",
			GovernanceGradeName: "Gouvernance",
			Requestable: []app.GrantableGrade{
				{ID: "gov-grade", Name: "Gouvernance"},
			},
		},
	})
	if err == nil {
		t.Fatal("api.New devrait échouer si le grade de gouvernance est requestable")
	}
	if !strings.Contains(err.Error(), "gouvernance") {
		t.Errorf("message d'erreur doit mentionner la gouvernance : %v", err)
	}
}

// TestSeedGrades_HookEnablesCustomGradeProfile prouve l'utilité du hook
// SeedGrades : un profil référençant un grade custom échoue au boot SANS le hook
// (le grade n'existe pas dans la table RBAC), et démarre AVEC le hook (qui seede
// le grade entre les migrations et la validation des profils).
func TestSeedGrades_HookEnablesCustomGradeProfile(t *testing.T) {
	t.Parallel()

	profiles := []signupprofile.Profile{
		{ID: "personne-autorisee", Label: "Personne autorisée", GradeID: "test-grade"},
	}

	// Sans le hook : le grade "test-grade" n'est pas seedé → boot échoue (fail-loud).
	_, err := api.New(api.Options{
		DBPath:         ":memory:",
		BaseURL:        "http://localhost",
		Port:           "0",
		SignupProfiles: profiles,
	})
	if err == nil {
		t.Fatal("sans SeedGrades, api.New devrait échouer (grade test-grade non seedé)")
	}
	if !strings.Contains(err.Error(), "test-grade") {
		t.Errorf("erreur attendue nommant le grade absent, obtenu : %v", err)
	}

	// Avec le hook : le grade est seedé après les migrations, avant la validation.
	app, err := api.New(api.Options{
		DBPath:         ":memory:",
		BaseURL:        "http://localhost",
		Port:           "0",
		SignupProfiles: profiles,
		SeedGrades: func(db *sql.DB) error {
			_, err := db.Exec(
				`INSERT INTO grades(id, name, system) VALUES(?, ?, 0)`,
				"test-grade", "Grade de test",
			)
			return err
		},
	})
	if err != nil {
		t.Fatalf("avec SeedGrades, api.New devrait réussir : %v", err)
	}
	if app == nil {
		t.Fatal("app nil malgré New réussi")
	}
}

// TestSeedGrades_HookErrorFailsBootNoLeak vérifie qu'une erreur du hook fait
// échouer New avec un message contenant "seed grades" (la db est refermée).
func TestSeedGrades_HookErrorFailsBootNoLeak(t *testing.T) {
	t.Parallel()

	app, err := api.New(api.Options{
		DBPath:  ":memory:",
		BaseURL: "http://localhost",
		Port:    "0",
		SeedGrades: func(*sql.DB) error {
			return errors.New("boom seed")
		},
	})
	if err == nil {
		t.Fatal("api.New devrait échouer quand SeedGrades renvoie une erreur")
	}
	if !strings.Contains(err.Error(), "seed grades") {
		t.Errorf("erreur attendue contenant \"seed grades\", obtenu : %v", err)
	}
	if app != nil {
		t.Errorf("app devrait être nil sur erreur de seed, obtenu : %v", app)
	}
}

// TestAPI_GracefulShutdownRespectsContext vérifie que le shutdown s'effectue
// en moins de 10s après ctx cancel.
func TestAPI_GracefulShutdownRespectsContext(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.ListenAndServe(ctx)
	}()

	// Laisser le serveur démarrer.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if elapsed > 10*time.Second {
			t.Errorf("shutdown a pris %v, want ≤ 10s", elapsed)
		}
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("ListenAndServe: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout: shutdown non reçu en 15s")
	}
}
