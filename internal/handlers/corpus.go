// CLAUDE:SUMMARY Handler de consultation du corpus juridique AFP : formulaire de
// requête (route authentifiée) interrogeant le fournisseur de corpus injecté à la
// bordure, et rendu des résultats sourcés (contenu + score + ancrage de provenance).
// État vide assumé : zéro résultat affiche un message neutre, aucun extrait inventé.
// Import-clean : aucune dépendance siftragclient ni horos55 — seul corpus.Provider
// (interface côté consommateur) est connu.
package handlers

import (
	"net/http"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/corpus"
)

// corpusSearchLimit borne le nombre de résultats demandés au fournisseur. Valeur
// conventionnelle pour une consultation interactive (assez large pour couvrir une
// recherche utile, assez courte pour rester lisible à l'écran).
const corpusSearchLimit = 10

// handleCorpusConsult gère GET sur /account/corpus : il affiche le formulaire de
// requête et, si une requête est présente (paramètre q), interroge le fournisseur
// de corpus injecté puis rend les résultats sourcés. Route gardée par requireAuth
// (consultation réservée aux membres authentifiés). Le fournisseur par défaut est
// inerte : sans moteur branché, la recherche retourne zéro résultat (jamais de
// contenu fabriqué).
func handleCorpusConsult(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))

		provider := deps.CorpusProvider
		if provider == nil {
			// Ceinture-et-bretelles : api.New garantit un provider non-nil
			// (InertProvider par défaut). Un montage de test minimal pourrait
			// l'omettre — retomber sur l'inerte plutôt que paniquer.
			provider = corpus.InertProvider{}
		}
		providerActive := !corpus.IsInert(provider)

		var hits []corpus.Hit
		var searched bool
		if query != "" && providerActive {
			searched = true
			res, err := provider.SearchCorpus(r.Context(), query, corpusSearchLimit)
			if err != nil {
				deps.Logger.Error("corpus_consult", "query", query, "err", err.Error())
				http.Error(w, "Erreur interne lors de la consultation du corpus.", http.StatusInternalServerError)
				return
			}
			hits = res
		}

		renderPageV2(w, r, deps, "Consultation du corpus juridique",
			views.CorpusConsultPage(query, providerActive, searched, hits))
	}
}
