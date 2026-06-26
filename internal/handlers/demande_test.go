// CLAUDE:SUMMARY Tests des parcours guidés de demande (V3c) : soumission membre +
// isolation IDOR (un membre ne voit que ses demandes), conversion administrative
// (convention créée + demande passée à « convertie ») et garde de reconversion
// (demande déjà convertie : action rejetée). Données via les stores réels.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/demande"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// setAdminUser place un administrateur en contexte (partagé avec onboarding_test).
func setAdminUser(ctx context.Context) context.Context {
	return middleware.ContextWithUser(ctx, &identity.User{ID: "admin1", Roles: []string{"admin"}})
}

// withChiID injecte un paramètre d'URL chi ({id}) dans le contexte de la requête.
func withChiID(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func demandeForm(values map[string]string) url.Values {
	f := url.Values{}
	for k, v := range values {
		f.Set(k, v)
	}
	return f
}

func postMember(deps app.AppDeps, h http.HandlerFunc, target, userID, name string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = memberReq(r, userID, name)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestDemandeMad_PostThenRead_AndIDOR : un membre soumet une demande puis la relit ;
// un autre membre ne voit jamais cette demande (garde IDOR rôle-scopée userID session).
func TestDemandeMad_PostThenRead_AndIDOR(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "mA", "ma@example.com", "Membre A")
	mkAccountUser(t, deps, "mB", "mb@example.com", "Membre B")

	w := postMember(deps, handleAccountDemandeMad(deps), "/account/demande-mad", "mA", "Membre A",
		demandeForm(map[string]string{
			"parcelle_ref": "09061 · ZD · 0007",
			"objet":        "Pâturage estival",
			"details":      "Estive juin-septembre.",
		}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("attendu 303 après POST, obtenu %d", w.Code)
	}

	// mA voit sa demande.
	rA := httptest.NewRequest(http.MethodGet, "/account/demande-mad", nil)
	rA = memberReq(rA, "mA", "Membre A")
	wA := httptest.NewRecorder()
	handleAccountDemandeMad(deps).ServeHTTP(wA, rA)
	if wA.Code != http.StatusOK || !strings.Contains(wA.Body.String(), "Pâturage estival") {
		t.Fatalf("mA doit voir sa demande; code=%d", wA.Code)
	}

	// mB ne voit PAS la demande de mA (IDOR).
	rB := httptest.NewRequest(http.MethodGet, "/account/demande-mad", nil)
	rB = memberReq(rB, "mB", "Membre B")
	wB := httptest.NewRecorder()
	handleAccountDemandeMad(deps).ServeHTTP(wB, rB)
	if strings.Contains(wB.Body.String(), "Pâturage estival") {
		t.Fatal("fuite IDOR : mB voit la demande de mA")
	}
}

// TestDemandeConversion_CreatesConventionAndMarksConverted : la conversion crée une
// convention via convention.Store.Create (réutilisé) puis passe la demande à
// « convertie ». Une seconde conversion de la même demande est rejetée (garde d'état).
func TestDemandeConversion_CreatesConventionAndMarksConverted(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "preneur1", "preneur1@example.com", "Preneur Un")

	// Parcelle réelle au registre, à rattacher à la convention.
	pid, err := (&parcelle.Store{DB: deps.DB}).Create(ctx, parcelle.Parcelle{
		CommuneCode: "09061", Section: "ZD", NumeroParcelle: "0007", SurfaceM2: 12000, StatutMad: "libre",
	})
	if err != nil {
		t.Fatalf("Create parcelle: %v", err)
	}
	// Demande réelle.
	demStore := &demande.Store{DB: deps.DB}
	demID, err := demStore.Create(ctx, demande.Demande{
		UserID: "preneur1", ParcelleRef: "09061 · ZD · 0007", Objet: "Pâturage estival",
	})
	if err != nil {
		t.Fatalf("Create demande: %v", err)
	}

	// preneur_id forgé : un membre différent de l'auteur de la demande. Le handler
	// doit l'ignorer et ancrer la convention sur d.UserID (preneur1).
	mkAccountUser(t, deps, "attaquant", "attaquant@example.com", "Membre Forgé")
	form := demandeForm(map[string]string{
		"preneur_id": "attaquant",
		"duree_mois": "60",
		"action":     "convertir",
	})
	form.Set("parcelle_id", pid)

	r := httptest.NewRequest(http.MethodPost, "/admin/demandes/"+demID+"/convertir", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withChiID(r, "id", demID)
	r = r.WithContext(setAdminUser(r.Context()))
	w := httptest.NewRecorder()
	handleAdminDemandeConvertir(deps).ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("attendu 303, obtenu %d", w.Code)
	}

	// Une convention a été créée pour le preneur, rattachée à la parcelle.
	convs, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, "preneur1")
	if err != nil {
		t.Fatalf("ListForUser conventions: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("attendu 1 convention créée, obtenu %d", len(convs))
	}
	if len(convs[0].Parcelles) != 1 || convs[0].Parcelles[0] != pid {
		t.Fatalf("la convention doit rattacher la parcelle %s, obtenu %+v", pid, convs[0].Parcelles)
	}
	if convs[0].PreneurID != "preneur1" {
		t.Fatalf("la convention doit être ancrée sur l'auteur de la demande (preneur1), obtenu %q", convs[0].PreneurID)
	}
	// Le preneur_id forgé du formulaire ne doit avoir créé aucune convention.
	forged, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, "attaquant")
	if err != nil {
		t.Fatalf("ListForUser conventions (attaquant): %v", err)
	}
	if len(forged) != 0 {
		t.Fatalf("le preneur_id forgé ne doit créer aucune convention, obtenu %d", len(forged))
	}

	// La demande est passée à « convertie ».
	d, err := demStore.GetByID(ctx, demID)
	if err != nil {
		t.Fatalf("GetByID demande: %v", err)
	}
	if d.Statut != demande.StatutConvertie {
		t.Fatalf("attendu statut convertie, obtenu %q", d.Statut)
	}

	// Reconversion : rejetée (garde d'état) ; aucune seconde convention créée.
	r2 := httptest.NewRequest(http.MethodPost, "/admin/demandes/"+demID+"/convertir", strings.NewReader(form.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2 = withChiID(r2, "id", demID)
	r2 = r2.WithContext(setAdminUser(r2.Context()))
	w2 := httptest.NewRecorder()
	handleAdminDemandeConvertir(deps).ServeHTTP(w2, r2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("reconversion : attendu 303 (redirect avec flash erreur), obtenu %d", w2.Code)
	}
	convs2, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, "preneur1")
	if err != nil {
		t.Fatalf("ListForUser conventions (post-reconversion): %v", err)
	}
	if len(convs2) != 1 {
		t.Fatalf("reconversion rejetée : la convention ne doit pas être dupliquée, obtenu %d", len(convs2))
	}
}
