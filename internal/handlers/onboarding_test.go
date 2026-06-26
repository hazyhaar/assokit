// CLAUDE:SUMMARY Tests du parcours guidé d'intégration d'un propriétaire (V3c) :
// cascade nominale (membre + adhésion + parcelle relus en base via les stores réels)
// et échec de validation (rien créé). Données créées via les stores réels, jamais
// hardcodées dans le code de prod.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

func adminOnboardReq(method, target string, form url.Values) *http.Request {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	return r.WithContext(setAdminUser(r.Context()))
}

// TestOnboard_NominalCascade : une intégration valide crée le membre, son adhésion et
// sa parcelle, tous relus en base via les stores réels.
func TestOnboard_NominalCascade(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()

	form := url.Values{}
	form.Set("display_name", "Jean Berger")
	form.Set("email", "jean.berger@example.com")
	form.Set("password", "motdepasse-initial")
	form.Set("period_start", "2026-01-01")
	form.Set("period_end", "2026-12-31")
	form.Set("amount_cents", "5000")
	form.Set("parcelle_commune", "09061")
	form.Set("parcelle_section", "ZD")
	form.Set("parcelle_numero", "0007")
	form.Set("parcelle_surface", "12000")
	form.Set("parcelle_nature", "pleine_propriete")

	r := adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", form)
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("attendu 303 après POST, obtenu %d", w.Code)
	}

	// Le membre existe et est authentifiable (identity.Authenticate).
	user, err := (&identity.Store{DB: deps.DB}).Authenticate(ctx, "jean.berger@example.com", "motdepasse-initial")
	if err != nil || user == nil {
		t.Fatalf("le membre intégré doit être relu en base et authentifiable: %v", err)
	}
	// L'adhésion existe.
	cotis, err := (&membership.Store{DB: deps.DB}).ListForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListForUser cotisations: %v", err)
	}
	if len(cotis) != 1 || cotis[0].AmountCents != 5000 {
		t.Fatalf("attendu 1 adhésion à 5000c, obtenu %+v", cotis)
	}
	// La parcelle existe et est rattachée au membre (ListForUser = jointure droit).
	parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListForUser parcelles: %v", err)
	}
	if len(parcs) != 1 || parcs[0].NumeroParcelle != "0007" || parcs[0].SurfaceM2 != 12000 {
		t.Fatalf("attendu 1 parcelle 0007/12000m² rattachée, obtenu %+v", parcs)
	}
}

// TestOnboard_ValidationFail_RienCree : une période invalide fait échouer la
// validation amont ; aucun membre, aucune parcelle ne sont créés.
func TestOnboard_ValidationFail_RienCree(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()

	form := url.Values{}
	form.Set("display_name", "Marie Sans Suite")
	form.Set("email", "marie@example.com")
	form.Set("password", "motdepasse-initial")
	form.Set("period_start", "01/01/2026") // format non ISO : invalide.
	form.Set("period_end", "31/12/2026")
	form.Set("parcelle_commune", "09061")
	form.Set("parcelle_section", "ZD")
	form.Set("parcelle_numero", "0007")
	form.Set("parcelle_surface", "12000")
	form.Set("parcelle_nature", "pleine_propriete")

	r := adminOnboardReq(http.MethodPost, "/admin/onboard-proprietaire", form)
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)

	// La validation amont rejette : aucun membre ne doit exister.
	_, err := (&identity.Store{DB: deps.DB}).Authenticate(ctx, "marie@example.com", "motdepasse-initial")
	if err == nil {
		t.Fatal("échec de validation : aucun membre ne devait être créé")
	}
}

// TestOnboard_GET_RendForm : le formulaire guidé rend 200 avec ses sections.
func TestOnboard_GET_RendForm(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	r := adminOnboardReq(http.MethodGet, "/admin/onboard-proprietaire", nil)
	w := httptest.NewRecorder()
	handleAdminOnboardProprietaire(deps).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Intégrer un propriétaire") {
		t.Fatal("le formulaire doit porter son titre")
	}
}
