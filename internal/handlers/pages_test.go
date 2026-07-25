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

// TestHome_AuthenticatedReturns200PageAccueil : un utilisateur connecté reçoit la
// page d'accueil (200), sans redirection vers /forum.
func TestHome_AuthenticatedReturns200PageAccueil(t *testing.T) {
	theme.Init(&theme.Branding{Name: "Test", BaseURL: "http://localhost", Texts: map[string]string{}})

	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	store := &tree.Store{DB: db}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{
		ID: "membre-1", Roles: []string{"member"},
	}))
	w := httptest.NewRecorder()
	handlePage(deps, "home", store).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200 page d'accueil, obtenu %d (Location=%q)", w.Code, w.Header().Get("Location"))
	}
	body := w.Body.String()
	if strings.Contains(body, "http-equiv") && strings.Contains(body, "/forum") {
		t.Fatal("attendu aucune redirection vers /forum")
	}
	if !strings.Contains(body, "Accueil") && !strings.Contains(body, "Bienvenue") {
		t.Fatalf("corps sans marqueur d'accueil, extrait=%q", body[:min(200, len(body))])
	}
}

// TestPage_EmptyCMSState : une page CMS sans contenu affiche un état vide honnête.
func TestPage_EmptyCMSState(t *testing.T) {
	theme.Init(&theme.Branding{Name: "Test", BaseURL: "http://localhost", Texts: map[string]string{}})

	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	store := &tree.Store{DB: db}

	r := httptest.NewRequest(http.MethodGet, "/faq", nil)
	w := httptest.NewRecorder()
	handlePage(deps, "faq", store).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Aucun contenu publié pour l'instant.") {
		t.Fatal("attendu message d'état vide honnête")
	}
}
