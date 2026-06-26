// CLAUDE:SUMMARY Tests de l'espace propriétaire (V3a) : accueil consolidé,
// cotisations (N réelles + état vide), attestation imprimable, mutation
// (enregistrement POST + relecture). Données créées via les stores réels, jamais
// hardcodées dans le code de prod.
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
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/mutation"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

func newAccountDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{DB: db, Logger: slog.Default()}
}

// mkAccountUser crée un membre réel en base et retourne son id.
func mkAccountUser(t *testing.T, deps app.AppDeps, id, email, name string) {
	t.Helper()
	_, err := deps.DB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,1)`,
		id, email, "x", name,
	)
	if err != nil {
		t.Fatalf("mkAccountUser %s: %v", id, err)
	}
}

func memberReq(r *http.Request, id, name string) *http.Request {
	return r.WithContext(middleware.ContextWithUser(r.Context(),
		&identity.User{ID: id, DisplayName: name, Roles: []string{"member"}}))
}

// TestAccountHome_Renders : l'accueil consolidé rend 200 pour un membre, agrégeant
// ses parcelles et cotisations réelles.
func TestAccountHome_Renders(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "m1", "m1@example.com", "Membre Un")

	pid, err := (&parcelle.Store{DB: deps.DB}).Create(ctx, parcelle.Parcelle{
		CommuneCode: "05015", Section: "AB", NumeroParcelle: "0123", SurfaceM2: 1000, StatutMad: "libre",
	})
	if err != nil {
		t.Fatalf("Create parcelle: %v", err)
	}
	if _, err := (&parcelle.Store{DB: deps.DB}).AddDroit(ctx, pid, "m1", "pleine_propriete"); err != nil {
		t.Fatalf("AddDroit: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r = memberReq(r, "m1", "Membre Un")
	w := httptest.NewRecorder()
	handleAccountHome(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Espace propriétaire") {
		t.Fatal("la page d'accueil doit afficher le titre de l'espace propriétaire")
	}
	if !strings.Contains(body, "/account/cotisations") {
		t.Fatal("la page d'accueil doit lier la vue cotisations")
	}
}

// TestAccountCotisations_ListsReal : N cotisations réelles créées via le store sont
// listées dans la vue.
func TestAccountCotisations_ListsReal(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "m2", "m2@example.com", "Membre Deux")

	ms := &membership.Store{DB: deps.DB}
	if _, err := ms.Create(ctx, membership.Membership{
		UserID: "m2", PeriodStart: "2025-01-01", PeriodEnd: "2025-12-31", AmountCents: 5000, Status: "active",
	}); err != nil {
		t.Fatalf("Create cotisation 1: %v", err)
	}
	if _, err := ms.Create(ctx, membership.Membership{
		UserID: "m2", PeriodStart: "2024-01-01", PeriodEnd: "2024-12-31", AmountCents: 4500, Status: "expired",
	}); err != nil {
		t.Fatalf("Create cotisation 2: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/account/cotisations", nil)
	r = memberReq(r, "m2", "Membre Deux")
	w := httptest.NewRecorder()
	handleAccountCotisations(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "50,00 €") || !strings.Contains(body, "45,00 €") {
		t.Fatalf("les deux montants réels doivent apparaître; body=%q", body)
	}
	if !strings.Contains(body, "actif") || !strings.Contains(body, "expiré") {
		t.Fatal("les statuts traduits doivent apparaître")
	}
}

// TestAccountCotisations_Empty : un membre sans cotisation obtient l'état vide neutre.
func TestAccountCotisations_Empty(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "m3", "m3@example.com", "Membre Trois")

	r := httptest.NewRequest(http.MethodGet, "/account/cotisations", nil)
	r = memberReq(r, "m3", "Membre Trois")
	w := httptest.NewRecorder()
	handleAccountCotisations(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Aucune cotisation") {
		t.Fatal("l'état vide doit afficher un message neutre")
	}
}

// TestAccountAttestation_Generated : l'attestation imprimable est générée avec le
// contenu réel du membre (identité + parcelle rattachée).
func TestAccountAttestation_Generated(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "m4", "m4@example.com", "Jeanne Propriétaire")

	pid, err := (&parcelle.Store{DB: deps.DB}).Create(ctx, parcelle.Parcelle{
		CommuneCode: "05061", Section: "ZC", NumeroParcelle: "0042", SurfaceM2: 8000, StatutMad: "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("Create parcelle: %v", err)
	}
	if _, err := (&parcelle.Store{DB: deps.DB}).AddDroit(ctx, pid, "m4", "pleine_propriete"); err != nil {
		t.Fatalf("AddDroit: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/account/attestation", nil)
	r = memberReq(r, "m4", "Jeanne Propriétaire")
	w := httptest.NewRecorder()
	handleAccountAttestation(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Attestation d'adhésion") {
		t.Fatal("l'attestation doit porter son titre")
	}
	if !strings.Contains(body, "Jeanne Propriétaire") {
		t.Fatal("l'attestation doit nommer le membre réel")
	}
	if !strings.Contains(body, "0042") {
		t.Fatal("l'attestation doit lister la parcelle réelle rattachée")
	}
}

// TestAccountMutation_PostThenRead : une déclaration POSTée est enregistrée puis
// relue dans la liste du membre (GET).
func TestAccountMutation_PostThenRead(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "m5", "m5@example.com", "Membre Cinq")

	form := url.Values{
		"parcelle_ref": {"05015 · AB · 0123"},
		"type":         {"succession"},
		"details":      {"Transmission aux héritiers."},
	}
	pr := httptest.NewRequest(http.MethodPost, "/account/mutation", strings.NewReader(form.Encode()))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pr = memberReq(pr, "m5", "Membre Cinq")
	pw := httptest.NewRecorder()
	handleAccountMutation(deps).ServeHTTP(pw, pr)

	if pw.Code != http.StatusSeeOther {
		t.Fatalf("POST attendu 303, obtenu %d", pw.Code)
	}

	// Relecture au niveau store : la déclaration est persistée.
	decls, err := (&mutation.Store{DB: deps.DB}).ListForUser(context.Background(), "m5")
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("attendu 1 déclaration persistée, obtenu %d", len(decls))
	}
	if decls[0].Type != "succession" || decls[0].ParcelleRef != "05015 · AB · 0123" {
		t.Fatalf("déclaration relue incorrecte: %+v", decls[0])
	}

	// Relecture au niveau vue : la déclaration apparaît dans le GET.
	gr := httptest.NewRequest(http.MethodGet, "/account/mutation", nil)
	gr = memberReq(gr, "m5", "Membre Cinq")
	gw := httptest.NewRecorder()
	handleAccountMutation(deps).ServeHTTP(gw, gr)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET attendu 200, obtenu %d", gw.Code)
	}
	if !strings.Contains(gw.Body.String(), "05015 · AB · 0123") {
		t.Fatal("la déclaration enregistrée doit apparaître dans la liste")
	}
}

// TestAdminMutations_ListsAll : la vue administration liste les déclarations de
// tous les membres.
func TestAdminMutations_ListsAll(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "m6", "m6@example.com", "Membre Six")
	if _, err := (&mutation.Store{DB: deps.DB}).Create(ctx, mutation.Declaration{
		UserID: "m6", ParcelleRef: "ref-admin", Type: "vente",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/admin/mutations", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(),
		&identity.User{ID: "boris", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	handleAdminMutations(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ref-admin") {
		t.Fatal("la vue admin doit lister la déclaration")
	}
}
