// CLAUDE:SUMMARY Tests de l'espace éleveur/preneur (V3b) : accueil élevage
// (conventions + parcelles mises à disposition réelles), déclaration d'entretien et
// signalement de problème (POST persiste + relecture GET), export PAC CSV (données
// réelles + en-tête seul si vide), isolation IDOR cross-membre des saisies, vue
// administration du journal de terrain. Données créées via les stores réels.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/fieldlog"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// seedPreneurParcelle crée une parcelle mise à disposition d'un membre via une
// convention réelle (preneur) + un droit parcellaire. Retourne l'id de parcelle.
func seedPreneurParcelle(t *testing.T, deps app.AppDeps, userID, commune, section, numero string, surface int64) string {
	t.Helper()
	ctx := context.Background()
	pid, err := (&parcelle.Store{DB: deps.DB}).Create(ctx, parcelle.Parcelle{
		CommuneCode: commune, Section: section, NumeroParcelle: numero, SurfaceM2: surface, StatutMad: "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("Create parcelle: %v", err)
	}
	if _, err := (&parcelle.Store{DB: deps.DB}).AddDroit(ctx, pid, userID, "pleine_propriete"); err != nil {
		t.Fatalf("AddDroit: %v", err)
	}
	if _, err := (&convention.Store{DB: deps.DB}).Create(ctx, convention.Convention{
		PreneurID: userID, Parcelles: []string{pid}, DureeMois: 60, Statut: "active",
	}); err != nil {
		t.Fatalf("Create convention: %v", err)
	}
	return pid
}

// TestElevageHome_Renders : l'accueil élevage rend 200 et affiche la convention et
// la parcelle réelles du preneur connecté.
func TestElevageHome_Renders(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e1", "e1@example.com", "Trifil 09 SASU")
	seedPreneurParcelle(t, deps, "e1", "09047", "AB", "0012", 50000)

	r := httptest.NewRequest(http.MethodGet, "/account/elevage", nil)
	r = memberReq(r, "e1", "Trifil 09 SASU")
	w := httptest.NewRecorder()
	handleAccountElevage(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Espace élevage") {
		t.Fatal("la page doit porter le titre de l'espace élevage")
	}
	if !strings.Contains(body, "0012") {
		t.Fatal("la parcelle réelle mise à disposition doit apparaître")
	}
	if !strings.Contains(body, "60 mois") {
		t.Fatal("la durée de la convention réelle doit apparaître")
	}
}

// TestElevageEntretien_PostThenRead : un entretien POSTé est persisté puis relu (GET).
func TestElevageEntretien_PostThenRead(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e2", "e2@example.com", "Membre Entretien")

	form := url.Values{
		"category":     {"cloture"},
		"parcelle_ref": {"09047 · AB · 0012"},
		"event_date":   {"2026-06-10"},
		"details":      {"Réfection clôture nord."},
	}
	pr := httptest.NewRequest(http.MethodPost, "/account/elevage/entretien", strings.NewReader(form.Encode()))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pr = memberReq(pr, "e2", "Membre Entretien")
	pw := httptest.NewRecorder()
	handleAccountElevageEntretien(deps).ServeHTTP(pw, pr)

	if pw.Code != http.StatusSeeOther {
		t.Fatalf("POST attendu 303, obtenu %d", pw.Code)
	}

	entries, err := (&fieldlog.Store{DB: deps.DB}).ListForUserKind(context.Background(), "e2", fieldlog.KindEntretien)
	if err != nil {
		t.Fatalf("ListForUserKind: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("attendu 1 entretien persisté, obtenu %d", len(entries))
	}
	if entries[0].Category != "cloture" || entries[0].Details != "Réfection clôture nord." {
		t.Fatalf("entretien relu incorrect: %+v", entries[0])
	}

	gr := httptest.NewRequest(http.MethodGet, "/account/elevage/entretien", nil)
	gr = memberReq(gr, "e2", "Membre Entretien")
	gw := httptest.NewRecorder()
	handleAccountElevageEntretien(deps).ServeHTTP(gw, gr)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET attendu 200, obtenu %d", gw.Code)
	}
	if !strings.Contains(gw.Body.String(), "09047 · AB · 0012") {
		t.Fatal("l'entretien enregistré doit apparaître dans la liste")
	}
}

// TestElevageProbleme_PostThenRead : un problème terrain POSTé est persisté puis relu.
func TestElevageProbleme_PostThenRead(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e3", "e3@example.com", "Membre Probleme")

	form := url.Values{
		"category":     {"gibier"},
		"parcelle_ref": {"09047 · ZC · 0099"},
		"event_date":   {"2026-06-12"},
		"details":      {"Dégâts de cervidés (Hemlinger)."},
	}
	pr := httptest.NewRequest(http.MethodPost, "/account/elevage/probleme", strings.NewReader(form.Encode()))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pr = memberReq(pr, "e3", "Membre Probleme")
	pw := httptest.NewRecorder()
	handleAccountElevageProbleme(deps).ServeHTTP(pw, pr)

	if pw.Code != http.StatusSeeOther {
		t.Fatalf("POST attendu 303, obtenu %d", pw.Code)
	}

	entries, err := (&fieldlog.Store{DB: deps.DB}).ListForUserKind(context.Background(), "e3", fieldlog.KindProbleme)
	if err != nil {
		t.Fatalf("ListForUserKind: %v", err)
	}
	if len(entries) != 1 || entries[0].Category != "gibier" {
		t.Fatalf("problème relu incorrect: %+v", entries)
	}

	gr := httptest.NewRequest(http.MethodGet, "/account/elevage/probleme", nil)
	gr = memberReq(gr, "e3", "Membre Probleme")
	gw := httptest.NewRecorder()
	handleAccountElevageProbleme(deps).ServeHTTP(gw, gr)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET attendu 200, obtenu %d", gw.Code)
	}
	if !strings.Contains(gw.Body.String(), "0099") {
		t.Fatal("le signalement enregistré doit apparaître dans la liste")
	}
}

// TestElevageFieldLog_IsolationCrossMembre : le membre A ne voit que ses propres
// déclarations dans le GET, jamais celles de B (garde IDOR ; kind forcé serveur).
func TestElevageFieldLog_IsolationCrossMembre(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "eA", "ea@example.com", "Éleveur A")
	mkAccountUser(t, deps, "eB", "eb@example.com", "Éleveur B")

	store := &fieldlog.Store{DB: deps.DB}
	if _, err := store.Create(ctx, fieldlog.Entry{UserID: "eA", Kind: fieldlog.KindEntretien, Category: "cloture", ParcelleRef: "REF-A-SECRET"}); err != nil {
		t.Fatalf("Create eA: %v", err)
	}
	if _, err := store.Create(ctx, fieldlog.Entry{UserID: "eB", Kind: fieldlog.KindEntretien, Category: "eau", ParcelleRef: "REF-B-SECRET"}); err != nil {
		t.Fatalf("Create eB: %v", err)
	}

	gr := httptest.NewRequest(http.MethodGet, "/account/elevage/entretien", nil)
	gr = memberReq(gr, "eA", "Éleveur A")
	gw := httptest.NewRecorder()
	handleAccountElevageEntretien(deps).ServeHTTP(gw, gr)
	if gw.Code != http.StatusOK {
		t.Fatalf("GET attendu 200, obtenu %d", gw.Code)
	}
	body := gw.Body.String()
	if !strings.Contains(body, "REF-A-SECRET") {
		t.Fatal("le membre A doit voir sa propre déclaration")
	}
	if strings.Contains(body, "REF-B-SECRET") {
		t.Fatal("fuite IDOR : le membre A voit la déclaration de B")
	}
}

// TestElevageExportPAC_Real : l'export CSV contient les parcelles réelles du membre.
func TestElevageExportPAC_Real(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e4", "e4@example.com", "Membre Export")
	seedPreneurParcelle(t, deps, "e4", "09047", "AB", "0012", 50000)

	r := httptest.NewRequest(http.MethodGet, "/account/elevage/export-pac.csv", nil)
	r = memberReq(r, "e4", "Membre Export")
	w := httptest.NewRecorder()
	handleAccountElevageExportPAC(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type CSV attendu, obtenu %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "commune_code") {
		t.Fatal("le CSV doit porter son en-tête")
	}
	if !strings.Contains(body, "09047") || !strings.Contains(body, "0012") {
		t.Fatal("le CSV doit contenir la parcelle réelle")
	}
	if !strings.Contains(body, "5.0000") {
		t.Fatalf("le CSV doit convertir 50000 m² en 5,0000 ha; body=%q", body)
	}
}

// TestElevageExportPAC_Empty : un membre sans parcelle obtient un CSV à en-tête seul.
func TestElevageExportPAC_Empty(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e5", "e5@example.com", "Membre Sans Parcelle")

	r := httptest.NewRequest(http.MethodGet, "/account/elevage/export-pac.csv", nil)
	r = memberReq(r, "e5", "Membre Sans Parcelle")
	w := httptest.NewRecorder()
	handleAccountElevageExportPAC(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "commune_code") {
		t.Fatal("le CSV vide doit conserver l'en-tête")
	}
	// Une seule ligne de données (l'en-tête, terminée par un saut de ligne) : pas
	// de ligne parcelle. Un seul "\n" dans le corps (en-tête + sa fin de ligne).
	if n := strings.Count(strings.TrimRight(body, "\n"), "\n"); n != 0 {
		t.Fatalf("CSV vide attendu en-tete seul, obtenu %d sauts de ligne internes", n)
	}
}

// TestElevageExportPAC_CSVInjectionNeutralisee : une parcelle dont un champ texte
// commence par un préfixe de formule ('=') est neutralisée à l'export (préfixée
// d'une apostrophe), empêchant l'exécution de formule dans Excel/LibreOffice.
func TestElevageExportPAC_CSVInjectionNeutralisee(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	mkAccountUser(t, deps, "e7", "e7@example.com", "Membre Injection")

	ctx := context.Background()
	pid, err := (&parcelle.Store{DB: deps.DB}).Create(ctx, parcelle.Parcelle{
		CommuneCode: "09047", Section: "AB", NumeroParcelle: "=SUM(1,1)", SurfaceM2: 50000, StatutMad: "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("Create parcelle: %v", err)
	}
	if _, err := (&parcelle.Store{DB: deps.DB}).AddDroit(ctx, pid, "e7", "pleine_propriete"); err != nil {
		t.Fatalf("AddDroit: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/account/elevage/export-pac.csv", nil)
	r = memberReq(r, "e7", "Membre Injection")
	w := httptest.NewRecorder()
	handleAccountElevageExportPAC(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	// La cellule ne doit pas commencer par '=' (ni nue, ni juste après le guillemet
	// d'échappement CSV) : elle doit être préfixée d'une apostrophe.
	if strings.Contains(body, ",=SUM(1,1)") || strings.Contains(body, "\"=SUM(1,1)") {
		t.Fatalf("la cellule de formule ne doit pas démarrer par '='; body=%q", body)
	}
	if !strings.Contains(body, "'=SUM(1,1)") {
		t.Fatalf("la cellule doit être neutralisée par une apostrophe; body=%q", body)
	}
}

// TestSanitizeCSVField : table de neutralisation des préfixes de formule.
func TestSanitizeCSVField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"09047", "09047"},
		{"", ""},
		{"=SUM(1,1)", "'=SUM(1,1)"},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@cmd", "'@cmd"},
		{"  =danger", "'  =danger"},
		{"\t-x", "'\t-x"},
		{"normal-au-milieu", "normal-au-milieu"},
	}
	for _, c := range cases {
		if got := sanitizeCSVField(c.in); got != c.want {
			t.Errorf("sanitizeCSVField(%q) = %q, attendu %q", c.in, got, c.want)
		}
	}
}

// TestAdminFieldReports_ListsAll : la vue administration liste les entrées de tous
// les membres, tous types confondus.
func TestAdminFieldReports_ListsAll(t *testing.T) {
	deps := newAccountDeps(t)
	defer deps.DB.Close()
	ctx := context.Background()
	mkAccountUser(t, deps, "e6", "e6@example.com", "Membre Six")
	store := &fieldlog.Store{DB: deps.DB}
	if _, err := store.Create(ctx, fieldlog.Entry{UserID: "e6", Kind: fieldlog.KindProbleme, Category: "danger", ParcelleRef: "REF-ADMIN"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/admin/field-reports", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(),
		&identity.User{ID: "boris", Roles: []string{"admin"}}))
	w := httptest.NewRecorder()
	handleAdminFieldReports(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "REF-ADMIN") {
		t.Fatal("la vue admin doit lister l'entrée")
	}
	// La colonne « Membre » affiche le nom résolu, pas l'UUID brut du store.
	if !strings.Contains(w.Body.String(), "Membre Six") {
		t.Fatal("la vue admin doit afficher le nom du membre, pas son identifiant")
	}
}
