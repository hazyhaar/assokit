// CLAUDE:SUMMARY Garde métier requireMetierGrade et résolution route→grade depuis
// deps.MetierTabs (liste blanche d'instance). Dérive de u.Roles (projection user_grades),
// sans requête DB supplémentaire.
package handlers

import (
	"net/http"
	"slices"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/templux"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// requireMetierGrade délègue à middleware.RequireMetierGrade (implémentation unique).
func requireMetierGrade(grade string) func(http.Handler) http.Handler {
	return middleware.RequireMetierGrade(grade)
}

// metierGradeForRoute retourne le grade métier associé à une route si elle figure
// dans la liste blanche d'instance (égalité exacte ou sous-chemin). En cas de
// chevauchement de préfixes (/account vs /account/elevage), la route la plus
// spécifique (la plus longue) l'emporte.
func metierGradeForRoute(tabs []app.MetierTab, route string) string {
	var grade string
	bestLen := -1
	for _, tab := range tabs {
		if route != tab.Route && !strings.HasPrefix(route, tab.Route+"/") {
			continue
		}
		if len(tab.Route) > bestLen {
			bestLen = len(tab.Route)
			grade = tab.Grade
		}
	}
	return grade
}

// withAccountAuth compose requireAuth et, si la route est déclarée dans MetierTabs,
// requireMetierGrade pour le grade correspondant. MetierTabs vide → requireAuth seul.
func withAccountAuth(deps app.AppDeps, route string) []func(http.Handler) http.Handler {
	chain := []func(http.Handler) http.Handler{requireAuth}
	if grade := metierGradeForRoute(deps.MetierTabs, route); grade != "" {
		chain = append(chain, requireMetierGrade(grade))
	}
	return chain
}

// visibleMetierTabs filtre la liste blanche d'instance aux onglets dont le grade
// est détenu par l'utilisateur (intersection u.Roles ∩ MetierTabs). Les entrées
// Hidden (garde par route sans onglet) ne sont jamais rendues.
func visibleMetierTabs(tabs []app.MetierTab, roles []string) []app.MetierTab {
	if len(tabs) == 0 {
		return nil
	}
	out := make([]app.MetierTab, 0, len(tabs))
	for _, tab := range tabs {
		if !tab.Hidden && slices.Contains(roles, tab.Grade) {
			out = append(out, tab)
		}
	}
	return out
}

// metierTabViews convertit les onglets visibles en vues templux pour la barre.
func metierTabViews(tabs []app.MetierTab, activeRoute string) []templux.MetierTabView {
	out := make([]templux.MetierTabView, 0, len(tabs))
	for _, tab := range tabs {
		out = append(out, templux.MetierTabView{
			Label:  tab.Label,
			Route:  tab.Route,
			Active: tab.Route == activeRoute || strings.HasPrefix(activeRoute, tab.Route+"/"),
		})
	}
	return out
}

// filterAccountCards masque les cartes dont la cible est un onglet métier déclaré
// mais dont le grade n'est pas détenu. Les routes non déclarées restent visibles.
func filterAccountCards(cards []views.AccountCardView, tabs []app.MetierTab, roles []string) []views.AccountCardView {
	if len(tabs) == 0 {
		return cards
	}
	out := make([]views.AccountCardView, 0, len(cards))
	for _, card := range cards {
		grade := metierGradeForRoute(tabs, card.Href)
		if grade == "" || slices.Contains(roles, grade) {
			out = append(out, card)
		}
	}
	return out
}

// filterDisabledModuleCards retire les cartes dont la cible appartient à un module
// d'espace membre désactivé pour l'instance. Aucun module désactivé → liste
// inchangée (rétro-compatible).
func filterDisabledModuleCards(cards []views.AccountCardView, disabled map[string]bool) []views.AccountCardView {
	if len(disabled) == 0 {
		return cards
	}
	out := make([]views.AccountCardView, 0, len(cards))
	for _, card := range cards {
		if moduleDisabled(disabled, card.Href) {
			continue
		}
		out = append(out, card)
	}
	return out
}
