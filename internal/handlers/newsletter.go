// CLAUDE:SUMMARY Handlers admin diffusions (vague 2 : renderPageV2 + views.NewsletterAdminPage).
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/newsletter"
)

// handleAdminNewsletters affiche la liste des diffusions et le formulaire de composition.
func handleAdminNewsletters(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &newsletter.Store{DB: deps.DB}
		items, err := store.List(r.Context())
		if err != nil {
			deps.Logger.Error("admin_newsletters_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Diffusions", views.NewsletterAdminPage(items, csrfToken))
	}
}

// handleAdminNewsletterCompose traite la création d'un brouillon de diffusion.
func handleAdminNewsletterCompose(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		store := &newsletter.Store{DB: deps.DB}
		_, err := store.Create(r.Context(), newsletter.Newsletter{
			Subject:   r.FormValue("subject"),
			BodyMD:    r.FormValue("body"),
			CreatedBy: u.ID,
		})
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
		}
		http.Redirect(w, r, "/admin/newsletters", http.StatusSeeOther)
	}
}

// handleAdminNewsletterSend envoie une diffusion à tous les membres actifs via
// l'outbox mail (best-effort), puis la marque comme envoyée.
func handleAdminNewsletterSend(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Mailer == nil {
			middleware.PushFlash(w, "error", "mailer non configuré")
			http.Redirect(w, r, "/admin/newsletters", http.StatusSeeOther)
			return
		}
		id := chi.URLParam(r, "id")
		store := &newsletter.Store{DB: deps.DB}
		n, err := store.Get(r.Context(), id)
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
			http.Redirect(w, r, "/admin/newsletters", http.StatusSeeOther)
			return
		}
		emails, err := store.ActiveMemberEmails(r.Context())
		if err != nil {
			deps.Logger.Error("admin_newsletter_send_emails", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		sent := 0
		for _, email := range emails {
			if err := deps.Mailer.Enqueue(r.Context(), email, n.Subject, n.BodyMD, n.BodyHTML); err != nil {
				deps.Logger.Error("admin_newsletter_enqueue", "to", email, "err", err.Error())
				continue
			}
			sent++
		}
		if err := store.MarkSent(r.Context(), n.ID, sent); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		}
		http.Redirect(w, r, "/admin/newsletters", http.StatusSeeOther)
	}
}
