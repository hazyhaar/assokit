// CLAUDE:SUMMARY Tests de sécurité O1 : requireMetierGrade (403 sans grade, passage
// avec grade) et filtrage de la barre d'onglets métier selon les grades détenus.
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

const testMetierGrade = "test-metier-x"
const testMetierLabel = "Profil test X"

func metierTestDeps(t *testing.T) app.AppDeps {
	t.Helper()
	deps := newAccountDeps(t)
	deps.MetierTabs = []app.MetierTab{{
		Grade: testMetierGrade,
		Label: testMetierLabel,
		Route: "/account/elevage",
	}}
	return deps
}

func memberReqRoles(r *http.Request, id, name string, roles []string) *http.Request {
	return r.WithContext(middleware.ContextWithUser(r.Context(),
		&identity.User{ID: id, DisplayName: name, Roles: roles}))
}

// TestRequireMetierGrade_ForbiddenWithoutGrade : authentifié sans le grade → 403.
func TestRequireMetierGrade_ForbiddenWithoutGrade(t *testing.T) {
	deps := metierTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "nog", "nog@example.com", "Sans grade")

	r := chi.NewRouter()
	r.With(withAccountAuth(deps, "/account/elevage")...).Get("/account/elevage", handleAccountElevage(deps))

	req := httptest.NewRequest(http.MethodGet, "/account/elevage", nil)
	req = memberReqRoles(req, "nog", "Sans grade", []string{"member"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("attendu 403, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Accès refusé") {
		t.Fatal("le corps doit indiquer l'accès refusé")
	}
}

// TestRequireMetierGrade_AllowedWithGrade : détenteur du grade → passage (200).
func TestRequireMetierGrade_AllowedWithGrade(t *testing.T) {
	deps := metierTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "okg", "okg@example.com", "Avec grade")

	r := chi.NewRouter()
	r.With(withAccountAuth(deps, "/account/elevage")...).Get("/account/elevage", handleAccountElevage(deps))

	req := httptest.NewRequest(http.MethodGet, "/account/elevage", nil)
	req = memberReqRoles(req, "okg", "Avec grade", []string{"member", testMetierGrade})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
}

// TestMetierGradeForRoute_LongestPrefix : /account/elevage doit résoudre le grade
// de l'onglet le plus spécifique, pas celui de /account.
func TestMetierGradeForRoute_LongestPrefix(t *testing.T) {
	tabs := []app.MetierTab{
		{Grade: "Propriétaire", Label: "Accueil", Route: "/account"},
		{Grade: testMetierGrade, Label: testMetierLabel, Route: "/account/elevage"},
	}
	if g := metierGradeForRoute(tabs, "/account/elevage"); g != testMetierGrade {
		t.Fatalf("attendu %q, obtenu %q", testMetierGrade, g)
	}
	if g := metierGradeForRoute(tabs, "/account"); g != "Propriétaire" {
		t.Fatalf("attendu Propriétaire, obtenu %q", g)
	}
}

// TestMetierTabBar_OnlyOwnedTabs : la barre n'affiche que les onglets détenus.
func TestMetierTabBar_OnlyOwnedTabs(t *testing.T) {
	deps := metierTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "tab1", "tab1@example.com", "Sans onglet")
	mkAccountUser(t, deps, "tab2", "tab2@example.com", "Avec onglet")

	// Utilisateur sans grade métier : pas d'onglet ni de carte élevage.
	reqNo := httptest.NewRequest(http.MethodGet, "/account", nil)
	reqNo = memberReqRoles(reqNo, "tab1", "Sans onglet", []string{"member"})
	wNo := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(wNo, reqNo)
	if wNo.Code != http.StatusOK {
		t.Fatalf("accueil sans grade : attendu 200, obtenu %d", wNo.Code)
	}
	bodyNo := wNo.Body.String()
	if strings.Contains(bodyNo, testMetierLabel) {
		t.Fatal("un non-détenteur ne doit pas voir l'onglet métier")
	}
	if strings.Contains(bodyNo, "/account/elevage") {
		t.Fatal("un non-détenteur ne doit pas voir la carte élevage")
	}

	// Utilisateur avec grade : onglet et carte visibles.
	reqYes := httptest.NewRequest(http.MethodGet, "/account", nil)
	reqYes = memberReqRoles(reqYes, "tab2", "Avec onglet", []string{"member", testMetierGrade})
	wYes := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(wYes, reqYes)
	if wYes.Code != http.StatusOK {
		t.Fatalf("accueil avec grade : attendu 200, obtenu %d", wYes.Code)
	}
	bodyYes := wYes.Body.String()
	if !strings.Contains(bodyYes, testMetierLabel) {
		t.Fatal("un détenteur doit voir l'onglet métier")
	}
	if !strings.Contains(bodyYes, "/account/elevage") {
		t.Fatal("un détenteur doit voir la carte élevage")
	}
}
