// CLAUDE:SUMMARY Tests gardiens conventions de pâturage : garde de route requireAuth
// sur /account/conventions, isolation preneur (un membre ne voit que les siennes),
// génération via le fournisseur inerte (aucune fausse clause).
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

func mkConvUser(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, id+"@example.org", "x", id, 1,
	); err != nil {
		t.Fatalf("mkConvUser %s: %v", id, err)
	}
}

func mkConvParcelle(t *testing.T, db *sql.DB, numero string) string {
	t.Helper()
	id, err := (&parcelle.Store{DB: db}).Create(context.Background(), parcelle.Parcelle{
		CommuneCode:    "05015",
		Section:        "AB",
		NumeroParcelle: numero,
		SurfaceM2:      8000,
		StatutMad:      "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("create parcelle: %v", err)
	}
	return id
}

func newConvDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{DB: db, Logger: slog.Default(), ConventionProvider: convention.InertProvider{}}
}

// TestAdminConventions_ValidCreation : un POST valide crée la convention.
func TestAdminConventions_ValidCreation(t *testing.T) {
	deps := newConvDeps(t)
	defer deps.DB.Close()
	mkConvUser(t, deps.DB, "preneur-1")
	p1 := mkConvParcelle(t, deps.DB, "0001")

	form := url.Values{
		"preneur_id":  {"preneur-1"},
		"duree_mois":  {"36"},
		"parcelle_id": {p1},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/conventions", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "boris", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	handleAdminConventions(deps).ServeHTTP(w, r)

	list, err := (&convention.Store{DB: deps.DB}).ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("POST valide : 1 convention attendue, obtenu %d", len(list))
	}
}

// TestMyConventions_Isolation : un preneur ne voit que ses propres conventions.
func TestMyConventions_Isolation(t *testing.T) {
	deps := newConvDeps(t)
	defer deps.DB.Close()
	mkConvUser(t, deps.DB, "preneur-1")
	mkConvUser(t, deps.DB, "preneur-2")
	p1 := mkConvParcelle(t, deps.DB, "0001")
	if _, err := (&convention.Store{DB: deps.DB}).Create(context.Background(), convention.Convention{
		PreneurID: "preneur-1", Parcelles: []string{p1},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/account/conventions", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "preneur-2", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handleMyConventions(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("preneur-2 attendu 200, obtenu %d", w.Code)
	}
	if strings.Contains(w.Body.String(), p1) {
		t.Fatal("preneur-2 ne doit pas voir la parcelle d'une convention dont il n'est pas preneur")
	}
}

// TestRequireAuthConventions_AnonymousRedirected : la route /account/conventions
// est gardée — un anonyme est renvoyé vers /login par le handler.
func TestRequireAuthConventions_AnonymousRedirected(t *testing.T) {
	deps := newConvDeps(t)
	defer deps.DB.Close()
	r := httptest.NewRequest(http.MethodGet, "/account/conventions", nil)
	w := httptest.NewRecorder()
	handleMyConventions(deps).ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("anonyme attendu 302, obtenu %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirection attendue vers /login, obtenu %q", loc)
	}
}
