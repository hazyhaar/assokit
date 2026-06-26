// CLAUDE:SUMMARY Tests gardiens registre parcellaire : garde de route requireAuth sur /account/parcelles, rejet fail-loud surface non numérique / négative, rejet statut hors énumération.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// TestRequireAuth_AnonymousRedirected vérifie que la garde de route requireAuth
// redirige un visiteur anonyme vers la page de connexion (302) sans exécuter le
// handler protégé.
func TestRequireAuth_AnonymousRedirected(t *testing.T) {
	called := false
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := requireAuth(protected)

	r := httptest.NewRequest(http.MethodGet, "/account/parcelles", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("anonyme attendu 302, obtenu %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirection attendue vers /login, obtenu %q", loc)
	}
	if called {
		t.Fatal("le handler protégé ne doit pas être exécuté pour un anonyme")
	}
}

// TestRequireAuth_AuthenticatedPasses vérifie qu'un utilisateur authentifié
// (sans rôle particulier) traverse la garde de route.
func TestRequireAuth_AuthenticatedPasses(t *testing.T) {
	called := false
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	handler := requireAuth(protected)

	r := httptest.NewRequest(http.MethodGet, "/account/parcelles", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "u1", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Fatalf("le handler protégé doit s'exécuter pour un membre authentifié (code %d)", w.Code)
	}
}

func newParcellesDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{DB: db, Logger: slog.Default()}
}

// adminUserCtx attache un utilisateur admin au contexte, requis par le handler.
func adminUserReq(r *http.Request) *http.Request {
	return r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "boris", Roles: []string{"admin"}}))
}

// postParcelle exécute handleAdminParcelles en POST avec les valeurs de formulaire
// fournies et retourne le nombre de parcelles en base après l'opération.
func postParcelle(t *testing.T, deps app.AppDeps, form url.Values) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/parcelles", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = adminUserReq(r)
	w := httptest.NewRecorder()
	handleAdminParcelles(deps).ServeHTTP(w, r)

	store := &parcelle.Store{DB: deps.DB}
	list, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	return len(list)
}

// TestAdminParcelles_SurfaceNonNumeriqueRejected : une surface non numérique ne
// crée aucune parcelle (fail-loud, plus de surface 0 silencieuse).
func TestAdminParcelles_SurfaceNonNumeriqueRejected(t *testing.T) {
	deps := newParcellesDeps(t)
	defer deps.DB.Close()
	n := postParcelle(t, deps, url.Values{
		"commune_code":    {"05015"},
		"section":         {"AB"},
		"numero_parcelle": {"0123"},
		"surface_m2":      {"beaucoup"},
		"statut_mad":      {"libre"},
	})
	if n != 0 {
		t.Fatalf("surface non numérique : aucune parcelle ne doit être créée, obtenu %d", n)
	}
}

// TestAdminParcelles_SurfaceNegativeRejected : une surface négative ne crée
// aucune parcelle.
func TestAdminParcelles_SurfaceNegativeRejected(t *testing.T) {
	deps := newParcellesDeps(t)
	defer deps.DB.Close()
	n := postParcelle(t, deps, url.Values{
		"commune_code":    {"05015"},
		"section":         {"AB"},
		"numero_parcelle": {"0124"},
		"surface_m2":      {"-50"},
		"statut_mad":      {"libre"},
	})
	if n != 0 {
		t.Fatalf("surface négative : aucune parcelle ne doit être créée, obtenu %d", n)
	}
}

// TestAdminParcelles_StatutHorsEnumRejected : un statut hors énumération est
// rejeté par le Store (ErrStatutInvalide), aucune parcelle créée.
func TestAdminParcelles_StatutHorsEnumRejected(t *testing.T) {
	deps := newParcellesDeps(t)
	defer deps.DB.Close()
	n := postParcelle(t, deps, url.Values{
		"commune_code":    {"05015"},
		"section":         {"AB"},
		"numero_parcelle": {"0125"},
		"surface_m2":      {"12000"},
		"statut_mad":      {"reservee"},
	})
	if n != 0 {
		t.Fatalf("statut hors énumération : aucune parcelle ne doit être créée, obtenu %d", n)
	}
}

// TestAdminParcelles_ValidCreation : un POST valide crée bien la parcelle.
func TestAdminParcelles_ValidCreation(t *testing.T) {
	deps := newParcellesDeps(t)
	defer deps.DB.Close()
	n := postParcelle(t, deps, url.Values{
		"commune_code":    {"05015"},
		"section":         {"AB"},
		"numero_parcelle": {"0126"},
		"surface_m2":      {"12000"},
		"statut_mad":      {"mise_a_disposition"},
	})
	if n != 1 {
		t.Fatalf("POST valide : 1 parcelle attendue, obtenu %d", n)
	}
}
