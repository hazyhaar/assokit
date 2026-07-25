// CLAUDE:SUMMARY Helper partagé de rendu page : enveloppe webui.Shell (templux).
package webui

import (
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// RenderPage rend content dans la coquille templux complète (webui.Shell).
func RenderPage(w http.ResponseWriter, r *http.Request, logger *slog.Logger, title string, content templ.Component) {
	renderPageShell(w, r, logger, title, content, Shell(title, content))
}

// RenderPageWide rend content dans la coquille pleine largeur (webui.ShellWide).
func RenderPageWide(w http.ResponseWriter, r *http.Request, logger *slog.Logger, title string, content templ.Component) {
	renderPageShell(w, r, logger, title, content, ShellWide(title, content))
}

func renderPageShell(w http.ResponseWriter, r *http.Request, logger *slog.Logger, title string, content templ.Component, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx := middleware.WithPageURI(r.Context(), r.RequestURI)
	if err := page.Render(ctx, w); err != nil {
		logger.Error("render page", "title", title, "err", err)
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}
