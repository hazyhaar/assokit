// CLAUDE:SUMMARY Handler /soutenir : iframe HelloAsso ou fallback contact + IBAN. User-aware (cache bouton Adhérer si déjà membre).
package handlers

import (
	"net/http"
	"slices"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
)

// donateAdhereSlug : profil redirigé depuis le bouton "Adhérer" quand HelloAsso
// URL absente. Doit exister dans config/profils.toml du silo (NPS profils
// valides : lanceur, media, asso, expert, partenaire, don).
const donateAdhereSlug = "don"

func handleDonatePage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		isMember := user != nil && slices.Contains(user.Roles, "member")
		renderPage(w, r, deps, "Soutenir",
			pages.Donate(deps.Config.HelloassoDonURL, deps.Config.HelloassoCotisationURL, deps.Config.BankIBAN, user, isMember, donateAdhereSlug))
	}
}
