// CLAUDE:SUMMARY Handler /contact : GET form + POST submit (mailer + flash redirect /merci).
package handlers

import (
	"net/http"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
)

func handleContactPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, deps, "Contact", pages.Contact())
	}
}

func handleContactSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		// honeypot anti-spam
		if r.FormValue("website") != "" {
			http.Redirect(w, r, "/merci", http.StatusSeeOther)
			return
		}
		nom := strings.TrimSpace(r.FormValue("nom"))
		email := strings.TrimSpace(r.FormValue("email"))
		sujet := strings.TrimSpace(r.FormValue("sujet"))
		message := strings.TrimSpace(r.FormValue("message"))
		if nom == "" || email == "" || message == "" {
			middleware.PushFlash(w, "error", "Champs obligatoires manquants.")
			http.Redirect(w, r, "/contact", http.StatusSeeOther)
			return
		}
		subject := sujet
		if subject == "" {
			subject = "Contact site Assokit"
		}
		bodyText := "De : " + nom + " <" + email + ">\n\n" + message
		bodyHTML := "<p><strong>De :</strong> " + nom + " &lt;" + email + "&gt;</p><p>" + strings.ReplaceAll(message, "\n", "<br/>") + "</p>"
		if deps.Mailer != nil {
			if err := deps.Mailer.Enqueue(r.Context(), deps.Config.AdminEmail, "[Contact] "+subject, bodyText, bodyHTML); err != nil {
				deps.Logger.Error("contact mailer enqueue", "err", err)
			}
		} else {
			deps.Logger.Info("contact (no mailer)", "from", email, "sujet", subject, "len", len(message))
		}
		http.Redirect(w, r, "/merci", http.StatusSeeOther)
	}
}
