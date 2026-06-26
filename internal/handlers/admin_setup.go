// CLAUDE:SUMMARY Handler UI menu admin Setup (vague 2 : renderPageV2 + views.SetupPage).
package handlers

import (
	"net/http"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/setup"
)

// handleAdminSetup affiche le diagnostic de configuration de l'instance.
func handleAdminSetup(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items := setup.Status(deps)
		renderPageV2(w, r, deps, "Configuration", views.SetupPage(items))
	}
}
