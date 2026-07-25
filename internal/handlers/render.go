// CLAUDE:SUMMARY Helper de rendu page : enveloppe webui.Shell (templux, vague 3 : renderPage legacy supprime).
package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui"
)

// renderPageV2 rend `content` dans le shell templux (webui.Shell).
func renderPageV2(w http.ResponseWriter, r *http.Request, deps app.AppDeps, title string, content templ.Component) {
	webui.RenderPage(w, r, deps.Logger, title, content)
}

// renderPageWide rend `content` dans le shell pleine largeur (webui.ShellWide).
func renderPageWide(w http.ResponseWriter, r *http.Request, deps app.AppDeps, title string, content templ.Component) {
	webui.RenderPageWide(w, r, deps.Logger, title, content)
}
