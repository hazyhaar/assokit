// CLAUDE:SUMMARY Tests de sécurité O3a : routes propriétaire-spécifiques gardées par
// withAccountAuth (403 sans grade, passage avec grade) et filtrage des cartes d'accueil.
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
)

const testProprietaireGrade = "test-proprietaire-o3a"
const testProprietaireLabel = "Profil propriétaire test"

func proprietaireTestDeps(t *testing.T) app.AppDeps {
	t.Helper()
	deps := newAccountDeps(t)
	deps.MetierTabs = []app.MetierTab{{
		Grade: testProprietaireGrade,
		Label: testProprietaireLabel,
		Route: "/account",
	}}
	return deps
}

func mountProprietaireRoutes(t *testing.T, deps app.AppDeps) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.With(withAccountAuth(deps, "/account/parcelles")...).Get("/account/parcelles", handleMyParcelles(deps))
	r.With(withAccountAuth(deps, "/account/mutation")...).Get("/account/mutation", handleAccountMutation(deps))
	r.With(withAccountAuth(deps, "/account/mutation")...).Post("/account/mutation", handleAccountMutation(deps))
	return r
}

// TestProprietaireParcelles_ForbiddenWithoutGrade : authentifié sans le grade → 403.
func TestProprietaireParcelles_ForbiddenWithoutGrade(t *testing.T) {
	deps := proprietaireTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "prop-nog", "prop-nog@example.com", "Sans propriétaire")

	r := mountProprietaireRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet, "/account/parcelles", nil)
	req = memberReqRoles(req, "prop-nog", "Sans propriétaire", []string{"member"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("attendu 403, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Accès refusé") {
		t.Fatal("le corps doit indiquer l'accès refusé")
	}
}

// TestProprietaireParcelles_AllowedWithGrade : détenteur du grade → passage (200).
func TestProprietaireParcelles_AllowedWithGrade(t *testing.T) {
	deps := proprietaireTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "prop-ok", "prop-ok@example.com", "Avec propriétaire")

	r := mountProprietaireRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet, "/account/parcelles", nil)
	req = memberReqRoles(req, "prop-ok", "Avec propriétaire", []string{"member", testProprietaireGrade})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
}

// TestProprietaireMutation_ForbiddenWithoutGrade : authentifié sans le grade → 403.
func TestProprietaireMutation_ForbiddenWithoutGrade(t *testing.T) {
	deps := proprietaireTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "mut-nog", "mut-nog@example.com", "Sans propriétaire")

	r := mountProprietaireRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet, "/account/mutation", nil)
	req = memberReqRoles(req, "mut-nog", "Sans propriétaire", []string{"member"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("attendu 403, obtenu %d", w.Code)
	}

	// La soumission (POST) suit la même chaîne de garde : 403 sans le grade.
	reqPost := httptest.NewRequest(http.MethodPost, "/account/mutation", nil)
	reqPost = memberReqRoles(reqPost, "mut-nog", "Sans propriétaire", []string{"member"})
	wPost := httptest.NewRecorder()
	r.ServeHTTP(wPost, reqPost)
	if wPost.Code != http.StatusForbidden {
		t.Fatalf("POST mutation sans grade : attendu 403, obtenu %d", wPost.Code)
	}
}

// TestProprietaireMutation_AllowedWithGrade : détenteur du grade → passage (200).
func TestProprietaireMutation_AllowedWithGrade(t *testing.T) {
	deps := proprietaireTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "mut-ok", "mut-ok@example.com", "Avec propriétaire")

	r := mountProprietaireRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet, "/account/mutation", nil)
	req = memberReqRoles(req, "mut-ok", "Avec propriétaire", []string{"member", testProprietaireGrade})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
}

// TestProprietaireHome_FiltersOwnerCards : les cartes parcelles et mutation sont masquées
// sans le grade et visibles avec le grade (cohérence filterAccountCards / MetierTabs).
func TestProprietaireHome_FiltersOwnerCards(t *testing.T) {
	deps := proprietaireTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "home-nog", "home-nog@example.com", "Sans propriétaire")
	mkAccountUser(t, deps, "home-ok", "home-ok@example.com", "Avec propriétaire")

	reqNo := httptest.NewRequest(http.MethodGet, "/account", nil)
	reqNo = memberReqRoles(reqNo, "home-nog", "Sans propriétaire", []string{"member"})
	wNo := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(wNo, reqNo)
	if wNo.Code != http.StatusOK {
		t.Fatalf("accueil sans grade : attendu 200, obtenu %d", wNo.Code)
	}
	bodyNo := wNo.Body.String()
	if strings.Contains(bodyNo, "/account/parcelles") {
		t.Fatal("un non-détenteur ne doit pas voir la carte parcelles")
	}
	if strings.Contains(bodyNo, "/account/mutation") {
		t.Fatal("un non-détenteur ne doit pas voir la carte mutation")
	}

	reqYes := httptest.NewRequest(http.MethodGet, "/account", nil)
	reqYes = memberReqRoles(reqYes, "home-ok", "Avec propriétaire", []string{"member", testProprietaireGrade})
	wYes := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(wYes, reqYes)
	if wYes.Code != http.StatusOK {
		t.Fatalf("accueil avec grade : attendu 200, obtenu %d", wYes.Code)
	}
	bodyYes := wYes.Body.String()
	if !strings.Contains(bodyYes, "/account/parcelles") {
		t.Fatal("un détenteur doit voir la carte parcelles")
	}
	if !strings.Contains(bodyYes, "/account/mutation") {
		t.Fatal("un détenteur doit voir la carte mutation")
	}
	// La carte « Mes profils » (auto-enrôlement) est visible au détenteur ; au
	// patron legacy « préfixe racine /account », elle subit la contamination
	// comme toute carte (le patron par routes exactes, testé plus bas, la rend
	// visible sans grade).
	if !strings.Contains(bodyYes, "/account/profils") {
		t.Fatal("la carte « Mes profils » doit être visible au détenteur du grade")
	}
}

// proprietaireSubrouteDeps : déclaration du grade par routes EXACTES (onglet
// visible sur parcelles + garde Hidden sur mutation), sans onglet sur la racine
// /account — le patron anti-contamination du hub (incident GAFP 2026-07-02).
func proprietaireSubrouteDeps(t *testing.T) app.AppDeps {
	t.Helper()
	deps := newAccountDeps(t)
	deps.MetierTabs = []app.MetierTab{
		{Grade: testProprietaireGrade, Label: testProprietaireLabel, Route: "/account/parcelles"},
		{Grade: testProprietaireGrade, Label: "Déclaration de mutation", Route: "/account/mutation", Hidden: true},
	}
	return deps
}

// TestProprietaireSubroutes_HubGenericCards : quand le grade est déclaré par
// routes exactes, le hub /account rend ses cartes génériques à un membre SANS
// grade métier (régression : le préfixe racine vidait toutes les cartes).
func TestProprietaireSubroutes_HubGenericCards(t *testing.T) {
	deps := proprietaireSubrouteDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "sub-nog", "sub-nog@example.com", "Sans grade")

	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req = memberReqRoles(req, "sub-nog", "Sans grade", []string{"member"})
	w := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("accueil sans grade : attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	for _, href := range []string{
		"/account/conventions", "/account/cotisations", "/account/attestation",
		"/account/corpus", "/account/data-download", "/account/profils",
	} {
		if !strings.Contains(body, href) {
			t.Fatalf("carte générique %s absente du hub pour un membre sans grade", href)
		}
	}
	for _, href := range []string{"/account/parcelles", "/account/mutation"} {
		if strings.Contains(body, href) {
			t.Fatalf("carte gardée %s visible sans le grade", href)
		}
	}
}

// TestProprietaireSubroutes_GuardsEffective : les gardes par route exacte restent
// efficaces (403 sans grade, 200 avec) sur parcelles et mutation.
func TestProprietaireSubroutes_GuardsEffective(t *testing.T) {
	deps := proprietaireSubrouteDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "sub-g", "sub-g@example.com", "Avec grade")

	r := mountProprietaireRoutes(t, deps)
	for _, route := range []string{"/account/parcelles", "/account/mutation"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		req = memberReqRoles(req, "sub-g", "Avec grade", []string{"member"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s sans grade : attendu 403, obtenu %d", route, w.Code)
		}
		req = httptest.NewRequest(http.MethodGet, route, nil)
		req = memberReqRoles(req, "sub-g", "Avec grade", []string{"member", testProprietaireGrade})
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s avec grade : attendu 200, obtenu %d", route, w.Code)
		}
	}
}

// TestVisibleMetierTabs_SkipsHidden : une entrée Hidden garde sa route mais ne
// produit jamais d'onglet, même pour le détenteur du grade.
func TestVisibleMetierTabs_SkipsHidden(t *testing.T) {
	tabs := []app.MetierTab{
		{Grade: testProprietaireGrade, Label: testProprietaireLabel, Route: "/account/parcelles"},
		{Grade: testProprietaireGrade, Label: "Déclaration de mutation", Route: "/account/mutation", Hidden: true},
	}
	visible := visibleMetierTabs(tabs, []string{testProprietaireGrade})
	if len(visible) != 1 || visible[0].Route != "/account/parcelles" {
		t.Fatalf("attendu le seul onglet parcelles, obtenu %+v", visible)
	}
}

// TestAdminDashboard_HidesMetierGuardedLink : cohérence lien↔route — le lien du
// tableau de bord admin vers une route gardée par un grade métier non détenu
// n'est pas rendu (régression : « Mes parcelles » menait à un 403 au cookie admin).
func TestAdminDashboard_HidesMetierGuardedLink(t *testing.T) {
	deps := proprietaireSubrouteDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "adm-1", "adm-1@example.com", "Admin")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = memberReqRoles(req, "adm-1", "Admin", []string{"admin"})
	w := httptest.NewRecorder()
	handleAdminDashboard(deps, nil).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard admin : attendu 200, obtenu %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "/account/parcelles") {
		t.Fatal("le lien vers /account/parcelles ne doit pas être rendu à un admin sans le grade")
	}

	// Instance sans onglet métier : le lien reste rendu (aucune régression pour
	// un consommateur qui n'ancre pas de grade sur ces routes).
	deps.MetierTabs = nil
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = memberReqRoles(req, "adm-1", "Admin", []string{"admin"})
	handleAdminDashboard(deps, nil).ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "/account/parcelles") {
		t.Fatal("sans MetierTabs, le lien « Mes parcelles » doit rester rendu")
	}
}
