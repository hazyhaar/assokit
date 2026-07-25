// CLAUDE:SUMMARY Handlers UI messagerie privée membre↔membre (vague 2 : renderPageV2 + views.Messages*).
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/messaging"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// handleMessagesInbox affiche la liste des conversations de l'utilisateur courant.
func handleMessagesInbox(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/messages", http.StatusFound)
			return
		}
		store := &messaging.Store{DB: deps.DB}
		convs, err := store.Inbox(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("messages_inbox", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Messages", views.MessagesInbox(convs))
	}
}

// handleMessagesThread affiche le fil avec un correspondant et marque les reçus lus.
func handleMessagesThread(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		withID := chi.URLParam(r, "userID")
		withName, ok := lookupMemberName(deps, withID)
		if !ok {
			http.NotFound(w, r)
			return
		}
		store := &messaging.Store{DB: deps.DB}
		msgs, err := store.Thread(r.Context(), u.ID, withID, 0)
		if err != nil {
			deps.Logger.Error("messages_thread", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		_, _ = store.MarkRead(r.Context(), u.ID, withID)
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, withName, views.MessagesThread(withID, withName, msgs, u.ID, csrfToken))
	}
}

// handleMessagesSend traite l'envoi d'un message depuis l'UI (POST /messages/{userID}).
func handleMessagesSend(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		withID := chi.URLParam(r, "userID")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		store := &messaging.Store{DB: deps.DB}
		if _, err := store.Send(r.Context(), u.ID, withID, r.FormValue("body")); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		}
		http.Redirect(w, r, "/messages/"+withID, http.StatusSeeOther)
	}
}

// lookupMemberName retourne le nom affichable d'un membre (display_name sinon
// email) et false s'il n'existe pas ou est inactif.
func lookupMemberName(deps app.AppDeps, userID string) (string, bool) {
	var name, email string
	var active int
	err := deps.DB.QueryRow(
		`SELECT display_name, email, is_active FROM users WHERE id=?`, userID,
	).Scan(&name, &email, &active)
	if err != nil || active == 0 {
		return "", false
	}
	if name != "" {
		return name, true
	}
	return email, true
}
