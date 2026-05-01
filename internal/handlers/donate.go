// CLAUDE:SUMMARY Handler /soutenir : iframe HelloAsso ou fallback contact + IBAN.
package handlers

import (
	"net/http"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
)

func handleDonatePage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, deps, "Soutenir",
			pages.Donate(deps.Config.HelloassoDonURL, deps.Config.HelloassoCotisationURL, deps.Config.BankIBAN))
	}
}
