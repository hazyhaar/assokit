// CLAUDE:SUMMARY Handlers UI adhésions/cotisations (vague 2 : renderPageV2 + views.Memberships*).
package handlers

import (
	"net/http"
	"strconv"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// handleAdminMemberships gère GET (liste + form) et POST (enregistrement) sur
// /admin/memberships. Gating admin attendu via requireAdmin sur la route.
func handleAdminMemberships(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &membership.Store{DB: deps.DB}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			cents, _ := strconv.ParseInt(r.FormValue("amount_cents"), 10, 64)
			_, err := store.Create(r.Context(), membership.Membership{
				UserID:      r.FormValue("user_id"),
				PeriodStart: r.FormValue("period_start"),
				PeriodEnd:   r.FormValue("period_end"),
				AmountCents: cents,
				Status:      r.FormValue("status"),
				Note:        r.FormValue("note"),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Adhésion enregistrée.")
			}
			http.Redirect(w, r, "/admin/memberships", http.StatusSeeOther)
			return
		}

		list, err := store.ListAll(r.Context(), r.URL.Query().Get("status"))
		if err != nil {
			deps.Logger.Error("admin_memberships", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		rows := make([]views.MembershipsAdminRow, 0, len(list))
		for _, m := range list {
			name, _ := lookupMemberName(deps, m.UserID)
			if name == "" {
				name = m.UserID
			}
			rows = append(rows, views.MembershipsAdminRow{Membership: m, MemberName: name})
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Adhésions", views.MembershipsAdminPage(rows, csrfToken))
	}
}

// handleAdminMembershipStatus traite POST /admin/memberships/status (changement
// de statut). Gating admin attendu via requireAdmin sur la route.
func handleAdminMembershipStatus(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		store := &membership.Store{DB: deps.DB}
		if err := store.SetStatus(r.Context(), r.FormValue("id"), r.FormValue("status")); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Statut mis à jour.")
		}
		http.Redirect(w, r, "/admin/memberships", http.StatusSeeOther)
	}
}

// handleMyMembership affiche les adhésions de l'utilisateur courant.
func handleMyMembership(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/membership", http.StatusFound)
			return
		}
		store := &membership.Store{DB: deps.DB}
		list, err := store.ListForUser(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("my_membership", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		current, err := store.CurrentStatus(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("my_membership_status", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Mon adhésion", views.MembershipsMyPage(current, list))
	}
}
