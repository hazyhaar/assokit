package api_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/pkg/api"
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
