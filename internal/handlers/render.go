// CLAUDE:SUMMARY Helpers de rendu page : enveloppe layout.Base + nav par défaut.
package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/layout"
)

// defaultNav : barre de navigation principale du site, identique sur toutes les pages.
// Centralisée ici pour éviter divergence entre handlers.
func defaultNav() []layout.NavItem {
	return []layout.NavItem{
		{Label: "Accueil", Href: "/"},
		{Label: "Charte", Href: "/charte"},
		{Label: "Thématiques", Href: "/thematiques"},
		{Label: "Médias", Href: "/medias"},
		{Label: "Forum", Href: "/forum"},
		{Label: "Soutenir", Href: "/soutenir"},
		{Label: "Contact", Href: "/contact"},
	}
}

// renderPage rend `content` dans le layout standard (theme + nav + flash + footer).
// Définit Content-Type, écrit en streaming. Renvoie une erreur 500 si le rendu échoue.
func renderPage(w http.ResponseWriter, r *http.Request, deps app.AppDeps, title string, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := layout.Base(*deps.Theme, title, defaultNav(), content)
	if err := page.Render(r.Context(), w); err != nil {
		deps.Logger.Error("render page", "title", title, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}
