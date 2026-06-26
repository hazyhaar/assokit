// CLAUDE:SUMMARY Helper de rendu page : enveloppe webui.Shell (templux, vague 3 : renderPage legacy supprime).
package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// renderPageV2 rend `content` dans le shell templux (webui.Shell).
func renderPageV2(w http.ResponseWriter, r *http.Request, deps app.AppDeps, title string, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx := middleware.WithPageURI(r.Context(), r.RequestURI)
	page := webui.Shell(title, content)
	if err := page.Render(ctx, w); err != nil {
		deps.Logger.Error("render page", "title", title, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}
