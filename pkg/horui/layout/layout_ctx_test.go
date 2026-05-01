package layout_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-h/templ"
	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/layout"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	db.Exec(`INSERT INTO roles(id,label) VALUES('member','Member') ON CONFLICT DO NOTHING`)
	return db
}

func newAuthStore(t *testing.T, db *sql.DB) *auth.Store {
	t.Helper()
	return &auth.Store{DB: db}
}

// TestFlashBannerWithMessages vérifie que FlashBanner rend les messages injectés par le middleware.
func TestFlashBannerWithMessages(t *testing.T) {
	// Crée un cookie flash
	w := httptest.NewRecorder()
	middleware.PushFlash(w, "success", "Connexion réussie")
	flashCookies := w.Result().Cookies()

	// Passe le cookie flash via le middleware Flash
	var renderedHTML string
	handler := middleware.Flash(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Rend FlashBanner avec le contexte du middleware Flash
		var buf bytes.Buffer
		if err := layout.FlashBanner().Render(r.Context(), &buf); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		renderedHTML = buf.String()
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range flashCookies {
		r.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r)

	if !contains(renderedHTML, "Connexion réussie") {
		t.Errorf("FlashBanner avec message: want 'Connexion réussie' in %q", renderedHTML)
	}
	if !contains(renderedHTML, "flash-success") {
		t.Errorf("FlashBanner: missing flash-success class in %q", renderedHTML)
	}
}

// TestHeaderWithUserLoggedIn vérifie le rendu du header avec un utilisateur connecté.
func TestHeaderWithUserLoggedIn(t *testing.T) {
	db := newTestDB(t)
	secret := []byte("test-secret")

	store := newAuthStore(t, db)
	u, err := store.Register(context.Background(), "layout@nps.fr", "pass", "LayoutUser")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Pose le cookie session
	w := httptest.NewRecorder()
	middleware.SetSessionCookie(w, u.ID, secret, false)
	sessionCookies := w.Result().Cookies()

	// Initialise le thème pour le header
	theme.Init(&theme.Branding{Name: "TestSite", Nav: []theme.NavItem{{Label: "Accueil", Slug: "/"}}})

	var renderedHTML string
	authMW := middleware.Auth(db, secret)
	handler := authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := layout.Header().Render(r.Context(), &buf); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		renderedHTML = buf.String()
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range sessionCookies {
		r.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r)

	if !contains(renderedHTML, "LayoutUser") {
		t.Errorf("Header with user: want display name in %q", renderedHTML)
	}
	if !contains(renderedHTML, "Déconnexion") {
		t.Errorf("Header with user: want logout link in %q", renderedHTML)
	}
}

func TestHeaderWithoutUser(t *testing.T) {
	theme.Init(&theme.Branding{Name: "TestSite", Nav: []theme.NavItem{{Label: "Accueil", Slug: "/"}}})

	var buf bytes.Buffer
	// Sans user dans ctx → branche else
	if err := layout.Header().Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Header: %v", err)
	}
	html := buf.String()
	if !contains(html, "/login") {
		t.Errorf("Header sans user: want /login in %q", html)
	}
}

// TestSidebarRendersNavAndContent vérifie que Sidebar rend les liens de nav
// et injecte le contenu dans la zone sidebar-content.
func TestSidebarRendersNavAndContent(t *testing.T) {
	nav := []layout.SidebarNavItem{
		{Label: "Tableau de bord", Href: "/dashboard"},
		{Label: "Paramètres", Href: "/settings"},
	}
	inner := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<p>contenu interne</p>")
		return err
	})

	var buf bytes.Buffer
	if err := layout.Sidebar(nav, inner).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Sidebar: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"sidebar-layout",
		"sidebar-nav",
		"/dashboard",
		"Tableau de bord",
		"/settings",
		"Paramètres",
		"sidebar-content",
		"contenu interne",
	} {
		if !contains(html, want) {
			t.Errorf("Sidebar: want %q in %q", want, html)
		}
	}
}
