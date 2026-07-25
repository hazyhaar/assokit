package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/identity"
	tree "github.com/hazyhaar/nodetree"
)

func TestShellHeader_AuthenticatedUserDropdown(t *testing.T) {
	theme.Init(&theme.Branding{
		Name:    "Test",
		BaseURL: "http://localhost",
		Nav: []theme.NavItem{
			{Slug: "/", Label: "Accueil", Order: 1},
			{Slug: "/forum", Label: "Dossiers", Order: 2},
		},
		Texts: map[string]string{},
	})

	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	store := &tree.Store{DB: db}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{
		ID: "admin-1", DisplayName: "Alice Admin", Roles: []string{"admin"},
	}))
	w := httptest.NewRecorder()
	handlePage(deps, "home", store).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-dropdown`,
		`data-dropdown-trigger`,
		`data-dropdown-panel`,
		`Alice Admin`,
		`Mon compte`,
		`Agenda`,
		`Gestion`,
		`Déconnexion`,
		`data-nav-drawer`,
		`/static/js/dropdown.js`,
		`/static/js/navdrawer.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("corps authentifié sans %q", want)
		}
	}
	for _, absent := range []string{
		`class="user-name`,
		`bg-transparent text-ink hover:bg-surface-muted">Gestion</a>`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("corps authentifié contient l'ancien markup plat %q", absent)
		}
	}
}

func TestShellHeader_MemberDropdownWithoutAdminLink(t *testing.T) {
	theme.Init(&theme.Branding{
		Name:    "Test",
		BaseURL: "http://localhost",
		Nav:     []theme.NavItem{{Slug: "/", Label: "Accueil", Order: 1}},
		Texts:   map[string]string{},
	})

	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	store := &tree.Store{DB: db}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{
		ID: "membre-1", DisplayName: "Bob Membre", Roles: []string{"member"},
	}))
	w := httptest.NewRecorder()
	handlePage(deps, "home", store).ServeHTTP(w, r)

	body := w.Body.String()
	if strings.Contains(body, ">Gestion</a>") {
		t.Fatal("membre non-admin ne doit pas voir le lien Gestion")
	}
	if !strings.Contains(body, "Bob Membre") || !strings.Contains(body, "Mon compte") {
		t.Fatal("dropdown membre attendu avec nom et Mon compte")
	}
}
