// CLAUDE:SUMMARY Helpers de rendu page : enveloppe layout.Base.
package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/layout"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// renderPage rend `content` dans le layout standard (theme + nav + flash + footer).
// Définit Content-Type, écrit en streaming. Renvoie une erreur 500 si le rendu échoue.
func renderPage(w http.ResponseWriter, r *http.Request, deps app.AppDeps, title string, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx := middleware.WithPageURI(r.Context(), r.RequestURI)
	page := layout.Base(title, content)
	if err := page.Render(ctx, w); err != nil {
		deps.Logger.Error("render page", "title", title, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}
