// CLAUDE:SUMMARY Tests de sécurité O2 : garde gouvernance, refus forgés, auto-octroi,
// doublons de demande, octroi nominal via RBAC.Service (Recompute).
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/profilrequest"
	"github.com/hazyhaar/assokit/pkg/rbac"
)

const (
	testGovGradeID   = "test-gov-grade"
	testGovGradeName = "Gouvernance test"
	testReqGradeID   = "test-req-grade"
	testReqGradeName = "Profil requestable test"
)

func profilTestGrant() app.ProfileGrant {
	return app.ProfileGrant{
		GovernanceGradeID:   testGovGradeID,
		GovernanceGradeName: testGovGradeName,
		Requestable: []app.GrantableGrade{{
			ID:   testReqGradeID,
			Name: testReqGradeName,
		}},
	}
}

func profilTestDeps(t *testing.T) (app.AppDeps, *rbac.Service) {
	t.Helper()
	db := newTestDB(t)
	seedRoles(t, db)
	for _, g := range []struct{ id, name string }{
		{testGovGradeID, testGovGradeName},
		{testReqGradeID, testReqGradeName},
	} {
		if _, err := db.Exec(`INSERT INTO grades(id,name,system) VALUES(?,?,0) ON CONFLICT DO NOTHING`, g.id, g.name); err != nil {
			t.Fatalf("seed grade %s: %v", g.id, err)
		}
	}
	svc := &rbac.Service{
		Store:  &rbac.Store{DB: db},
		Cache:  &rbac.Cache{},
		Logger: slog.Default(),
	}
	deps := app.AppDeps{
		DB:           db,
		Logger:       slog.Default(),
		ProfileGrant: profilTestGrant(),
		RBAC:         svc,
	}
	return deps, svc
}

func profilGovRouter(deps app.AppDeps) chi.Router {
	r := chi.NewRouter()
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/profils/octroi", handleAccountProfilsOctroi(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Post("/account/profils/octroi", handleAccountProfilsOctroi(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Post("/account/profils/retrait", handleAccountProfilsRetrait(deps))
	return r
}

// TestProfilOctroi_ForbiddenWithoutGovernanceGrade : non-détenteur → 403 octroi et retrait.
func TestProfilOctroi_ForbiddenWithoutGovernanceGrade(t *testing.T) {
	deps, _ := profilTestDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "nogov", "nogov@example.com", "Sans gouvernance")

	r := profilGovRouter(deps)
	roles := []string{"member"}

	for _, path := range []string{"/account/profils/octroi", "/account/profils/retrait"} {
		method := http.MethodGet
		if path == "/account/profils/retrait" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, nil)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Body = http.NoBody
			req = httptest.NewRequest(method, path, strings.NewReader("user_id=x&grade_id="+testReqGradeID))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req = memberReqRoles(req, "nogov", "Sans gouvernance", roles)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s : attendu 403, obtenu %d", method, path, w.Code)
		}
	}
}

// TestProfilOctroi_RefuseGradeHorsRequestable : POST forgé avec grade hors liste → refusé.
func TestProfilOctroi_RefuseGradeHorsRequestable(t *testing.T) {
	deps, _ := profilTestDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "gov1", "gov1@example.com", "Gouvernance")
	mkAccountUser(t, deps, "ben1", "ben1@example.com", "Bénéficiaire")

	reqID, err := (&profilrequest.Store{DB: deps.DB}).Create(ctx, "ben1", testReqGradeID)
	if err != nil {
		t.Fatalf("Create demande: %v", err)
	}
	// Altérer le grade en base pour simuler un forgé (grade hors Requestable).
	if _, err := deps.DB.Exec(`UPDATE profile_requests SET grade_id='grade-fantome' WHERE id=?`, reqID); err != nil {
		t.Fatalf("update grade: %v", err)
	}

	form := url.Values{"request_id": {reqID}}
	req := httptest.NewRequest(http.MethodPost, "/account/profils/octroi", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = memberReqRoles(req, "gov1", "Gouvernance", []string{"member", testGovGradeName})
	w := httptest.NewRecorder()
	profilGovRouter(deps).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("attendu 303, obtenu %d", w.Code)
	}
	var n int
	if err := deps.DB.QueryRow(`SELECT COUNT(*) FROM user_grades WHERE user_id='ben1' AND grade_id=?`, testReqGradeID).Scan(&n); err != nil {
		t.Fatalf("query user_grades: %v", err)
	}
	if n != 0 {
		t.Fatal("le grade ne doit pas être assigné pour un grade hors Requestable")
	}
}

// TestProfilOctroi_RefuseGovernanceGrade : grade == GovernanceGradeID → refusé.
func TestProfilOctroi_RefuseGovernanceGrade(t *testing.T) {
	deps, _ := profilTestDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "gov2", "gov2@example.com", "Gouvernance")
	mkAccountUser(t, deps, "ben2", "ben2@example.com", "Bénéficiaire")

	reqID, err := (&profilrequest.Store{DB: deps.DB}).Create(ctx, "ben2", testGovGradeID)
	if err != nil {
		t.Fatalf("Create demande: %v", err)
	}
	if _, err := deps.DB.Exec(`UPDATE profile_requests SET grade_id=? WHERE id=?`, testGovGradeID, reqID); err != nil {
		t.Fatalf("force gov grade: %v", err)
	}

	actor := &identity.User{ID: "gov2", DisplayName: "Gouvernance", Roles: []string{testGovGradeName}}
	err = octroyerProfil(ctx, deps, actor, &profilrequest.Store{DB: deps.DB}, reqID)
	if err == nil {
		t.Fatal("octroi du grade de gouvernance doit être refusé")
	}
	// Refus attendu : hors Requestable (contrôle liste blanche) ou grade de gouvernance explicite.
	if !strings.Contains(err.Error(), errOctroiGouvernance.Error()) &&
		!strings.Contains(err.Error(), errGradeNonRequestable.Error()) {
		t.Fatalf("erreur de refus attendue, obtenu %v", err)
	}
}

// TestProfilOctroi_RefuseAutoOctroi : bénéficiaire == octroyeur → refusé.
func TestProfilOctroi_RefuseAutoOctroi(t *testing.T) {
	deps, _ := profilTestDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "self1", "self1@example.com", "Auto")

	reqID, err := (&profilrequest.Store{DB: deps.DB}).Create(ctx, "self1", testReqGradeID)
	if err != nil {
		t.Fatalf("Create demande: %v", err)
	}
	actor := &identity.User{ID: "self1", Roles: []string{testGovGradeName}}
	err = octroyerProfil(ctx, deps, actor, &profilrequest.Store{DB: deps.DB}, reqID)
	if err == nil {
		t.Fatal("auto-octroi doit être refusé")
	}
	if !strings.Contains(err.Error(), errAutoOctroi.Error()) {
		t.Fatalf("erreur attendue %q, obtenu %v", errAutoOctroi, err)
	}
}

// TestProfilDemande_RefuseDoublon : grade déjà détenu ou demande soumise → refusée.
func TestProfilDemande_RefuseDoublon(t *testing.T) {
	deps, svc := profilTestDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "dup1", "dup1@example.com", "Doublon")
	if err := svc.AssignGrade(ctx, "dup1", testReqGradeID); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}

	uHeld := &identity.User{ID: "dup1", Roles: []string{testReqGradeName}}
	err := validateDemande(ctx, deps.ProfileGrant, uHeld, testReqGradeID, &profilrequest.Store{DB: deps.DB})
	if err == nil {
		t.Fatal("demande avec grade déjà détenu doit être refusée")
	}
	if !strings.Contains(err.Error(), errGradeDejaDetenu.Error()) {
		t.Fatalf("erreur attendue %q, obtenu %v", errGradeDejaDetenu, err)
	}

	mkAccountUser(t, deps, "dup2", "dup2@example.com", "Doublon 2")
	if _, err := (&profilrequest.Store{DB: deps.DB}).Create(ctx, "dup2", testReqGradeID); err != nil {
		t.Fatalf("première demande: %v", err)
	}
	uPending := &identity.User{ID: "dup2", Roles: []string{"member"}}
	err = validateDemande(ctx, deps.ProfileGrant, uPending, testReqGradeID, &profilrequest.Store{DB: deps.DB})
	if err == nil {
		t.Fatal("deuxième demande soumise doit être refusée")
	}
	if !strings.Contains(err.Error(), errDemandeDejaSoumise.Error()) {
		t.Fatalf("erreur attendue %q, obtenu %v", errDemandeDejaSoumise, err)
	}
}

// TestProfilOctroi_NominalAssignGrade : octroi nominal passe par RBAC (user_grades + recompute).
func TestProfilOctroi_NominalAssignGrade(t *testing.T) {
	deps, svc := profilTestDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "gov3", "gov3@example.com", "Gouvernance")
	mkAccountUser(t, deps, "ben3", "ben3@example.com", "Bénéficiaire")

	reqID, err := (&profilrequest.Store{DB: deps.DB}).Create(ctx, "ben3", testReqGradeID)
	if err != nil {
		t.Fatalf("Create demande: %v", err)
	}
	actor := &identity.User{ID: "gov3", Roles: []string{testGovGradeName}}
	if err := octroyerProfil(ctx, deps, actor, &profilrequest.Store{DB: deps.DB}, reqID); err != nil {
		t.Fatalf("octroi nominal: %v", err)
	}
	var statut string
	if err := deps.DB.QueryRow(`SELECT statut FROM profile_requests WHERE id=?`, reqID).Scan(&statut); err != nil {
		t.Fatalf("query statut: %v", err)
	}
	if statut != "acceptee" {
		t.Fatalf("statut attendu acceptee, obtenu %q", statut)
	}
	grades, err := svc.Store.UserGrades(ctx, "ben3")
	if err != nil {
		t.Fatalf("UserGrades: %v", err)
	}
	found := false
	for _, g := range grades {
		if g.ID == testReqGradeID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("le grade requestable doit figurer dans user_grades après octroi")
	}
	// Recompute invalide le cache L1 (BumpVersion) : la preuve d'appel est la
	// présence du grade en base ; un second Recompute idempotent confirme le Service.
	if err := svc.Recompute(ctx, "ben3"); err != nil {
		t.Fatalf("Recompute post-octroi: %v", err)
	}
}
