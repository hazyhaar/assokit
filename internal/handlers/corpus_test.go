// CLAUDE:SUMMARY Tests gardiens consultation du corpus juridique : rendu des
// résultats sourcés via un fournisseur STUB (contenu + score + ancrage affichés),
// état vide assumé (requête sans résultat affiche un message neutre, aucune
// invention), et garde de route requireAuth sur /account/corpus.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/corpus"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// stubCorpusProvider est un fournisseur de corpus de test : il retourne une liste
// fixe de hits, en enregistrant la requête et la limite reçues pour vérifier la
// propagation. Aucun appel réseau, aucun moteur réel.
type stubCorpusProvider struct {
	hits      []corpus.Hit
	gotQuery  string
	gotLimit  int
	callCount int
}

func (s *stubCorpusProvider) SearchCorpus(_ context.Context, query string, limit int) ([]corpus.Hit, error) {
	s.gotQuery = query
	s.gotLimit = limit
	s.callCount++
	return s.hits, nil
}

func newCorpusDeps(t *testing.T, provider corpus.Provider) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{DB: db, Logger: slog.Default(), CorpusProvider: provider}
}

// TestCorpusConsult_ResultatsSources : une requête remonte les hits du fournisseur
// avec leur contenu, leur score et leur ancrage de provenance (résultats sourcés).
func TestCorpusConsult_ResultatsSources(t *testing.T) {
	stub := &stubCorpusProvider{hits: []corpus.Hit{
		{Content: "Article L135-3 du code rural", Score: 0.912, Ref: "afp_droit_chunk_000142", Source: "LEGIARTI000022219346"},
		{Content: "Redevances et titres de recettes", Score: 0.734, Ref: "afp_droit_chunk_000287", Source: ""},
	}}
	deps := newCorpusDeps(t, stub)
	defer deps.DB.Close()

	r := httptest.NewRequest(http.MethodGet, "/account/corpus?q=redevance", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "membre-1", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handleCorpusConsult(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if stub.callCount != 1 {
		t.Fatalf("fournisseur attendu appelé 1 fois, obtenu %d", stub.callCount)
	}
	if stub.gotQuery != "redevance" {
		t.Errorf("query propagée = %q, attendu redevance", stub.gotQuery)
	}
	if stub.gotLimit != corpusSearchLimit {
		t.Errorf("limit propagée = %d, attendu %d", stub.gotLimit, corpusSearchLimit)
	}
	body := w.Body.String()
	for _, want := range []string{"Article L135-3 du code rural", "afp_droit_chunk_000142", "0.912", "LEGIARTI000022219346"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendu sans %q", want)
		}
	}
}

// TestCorpusConsult_EtatVide : une requête sans résultat affiche un message neutre
// et ne fabrique aucun extrait.
func TestCorpusConsult_EtatVide(t *testing.T) {
	deps := newCorpusDeps(t, &stubCorpusProvider{hits: []corpus.Hit{}})
	defer deps.DB.Close()

	r := httptest.NewRequest(http.MethodGet, "/account/corpus?q=introuvable", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "membre-1", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handleCorpusConsult(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Aucun extrait du corpus") {
		t.Fatal("attendu un message d'état vide neutre")
	}
}

// TestCorpusConsult_ProviderInerte : sans moteur branché, la vue affiche un état
// honnête et aucun placeholder d'exemples prometteurs.
func TestCorpusConsult_ProviderInerte(t *testing.T) {
	deps := newCorpusDeps(t, corpus.InertProvider{})
	defer deps.DB.Close()

	r := httptest.NewRequest(http.MethodGet, "/account/corpus", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "membre-1", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handleCorpusConsult(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "n'est pas raccordé sur cette instance") {
		t.Fatal("attendu message honnête corpus non raccordé")
	}
	for _, forbidden := range []string{"redevance", "droit de délaissement", "enquête publique"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("placeholder prometteur interdit %q présent", forbidden)
		}
	}
}

// TestCorpusConsult_SansRequete : sans paramètre q, le fournisseur n'est pas appelé
// (formulaire seul affiché).
func TestCorpusConsult_SansRequete(t *testing.T) {
	stub := &stubCorpusProvider{}
	deps := newCorpusDeps(t, stub)
	defer deps.DB.Close()

	r := httptest.NewRequest(http.MethodGet, "/account/corpus", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), &identity.User{ID: "membre-1", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	handleCorpusConsult(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	if stub.callCount != 0 {
		t.Fatalf("sans requête, fournisseur ne doit pas être appelé (obtenu %d appels)", stub.callCount)
	}
}
