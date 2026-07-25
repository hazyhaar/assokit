// CLAUDE:SUMMARY Tests de sécurité O3c : garde gouvernance sur /account/gouvernance,
// accès lecture seule pour détenteur du grade, vue d'ensemble (état vide accepté).
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/governance"
)

const testAccountGovGradeName = "Gouvernance test O3c"

func accountGouvernanceDeps(t *testing.T) app.AppDeps {
	t.Helper()
	deps := newAccountDeps(t)
	deps.ProfileGrant = app.ProfileGrant{
		GovernanceGradeID:   "test-gov-grade-o3c",
		GovernanceGradeName: testAccountGovGradeName,
	}
	return deps
}

func accountGouvernanceRouter(deps app.AppDeps) chi.Router {
	r := chi.NewRouter()
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/gouvernance", handleAccountGouvernance(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/gouvernance/{id}/pv", handleAccountGouvernancePV(deps))
	return r
}

// TestAccountGouvernance_ForbiddenWithoutGrade : membre authentifié sans grade → 403.
func TestAccountGouvernance_ForbiddenWithoutGrade(t *testing.T) {
	deps := accountGouvernanceDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "nogov3c", "nogov3c@example.com", "Sans gouvernance")

	req := httptest.NewRequest(http.MethodGet, "/account/gouvernance", nil)
	req = memberReqRoles(req, "nogov3c", "Sans gouvernance", []string{"member"})
	w := httptest.NewRecorder()
	accountGouvernanceRouter(deps).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("attendu 403, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Accès refusé") {
		t.Fatal("le corps doit indiquer l'accès refusé")
	}
}

// TestAccountGouvernance_AllowedWithGrade : détenteur du grade → 200 et vue d'ensemble.
func TestAccountGouvernance_AllowedWithGrade(t *testing.T) {
	deps := accountGouvernanceDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "gov3c", "gov3c@example.com", "Avec gouvernance")

	req := httptest.NewRequest(http.MethodGet, "/account/gouvernance", nil)
	req = memberReqRoles(req, "gov3c", "Avec gouvernance", []string{"member", testAccountGovGradeName})
	w := httptest.NewRecorder()
	accountGouvernanceRouter(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Gouvernance") {
		t.Fatal("la page doit afficher le titre Gouvernance")
	}
	if !strings.Contains(body, "Assemblées générales") {
		t.Fatal("la page doit afficher la section assemblées")
	}
	if !strings.Contains(body, "Gestion des profils") {
		t.Fatal("la page doit lier la gestion des profils")
	}
	if !strings.Contains(body, "/account/profils/octroi") {
		t.Fatal("la page doit renvoyer vers l'octroi de profils")
	}
}

// TestAccountGouvernance_ListeAssembléeRéelle : une assemblée en base apparaît dans la liste.
func TestAccountGouvernance_ListeAssembléeRéelle(t *testing.T) {
	deps := accountGouvernanceDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "gov3c2", "gov3c2@example.com", "Gouvernance liste")

	asmID, err := (&governance.Store{DB: deps.DB}).Create(ctx, governance.Assembly{
		Name:        "AG extraordinaire test",
		ScheduledAt: "2026-09-12 18:00",
	})
	if err != nil {
		t.Fatalf("Create assembly: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/account/gouvernance", nil)
	req = memberReqRoles(req, "gov3c2", "Gouvernance liste", []string{"member", testAccountGovGradeName})
	w := httptest.NewRecorder()
	accountGouvernanceRouter(deps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "AG extraordinaire test") {
		t.Fatal("l'assemblée créée doit figurer dans la liste")
	}
	if !strings.Contains(body, "2026-09-12 18:00") {
		t.Fatal("la date programmée doit figurer")
	}
	if strings.Contains(body, "/account/gouvernance/"+asmID+"/pv") {
		t.Fatal("le lien PV ne doit pas apparaître sans procès-verbal généré")
	}
}
