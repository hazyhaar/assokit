// CLAUDE:SUMMARY Handlers UI gouvernance membre (O3c) : vue d'ensemble des
// assemblées et consultation des procès-verbaux en lecture seule. Garde par grade
// de gouvernance injecté (ProfileGrant.GovernanceGradeName), distincte de /admin.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// handleAccountGouvernance rend la vue d'ensemble des assemblées pour un membre
// détenteur du grade de gouvernance. Lecture seule : aucune mutation de gouvernance.
func handleAccountGouvernance(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/gouvernance", http.StatusFound)
			return
		}
		ctx := r.Context()
		store := &governance.Store{DB: deps.DB}
		list, err := store.ListAll(ctx)
		if err != nil {
			deps.Logger.Error("account_gouvernance_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		rows := make([]views.AccountAssemblyRowView, 0, len(list))
		for _, a := range list {
			hasMinutes := false
			if _, err := store.GetMinutes(ctx, a.ID); err == nil {
				hasMinutes = true
			} else if !errors.Is(err, governance.ErrMinutesIntrouvable) {
				deps.Logger.Error("account_gouvernance_minutes_probe", "assembly_id", a.ID, "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
			rows = append(rows, views.AccountAssemblyRowView{
				ID:          a.ID,
				Name:        a.Name,
				ScheduledAt: governanceScheduledLabel(a),
				StatusLabel: governanceStatusLabel(a.Status),
				HasMinutes:  hasMinutes,
			})
		}
		logAccountGovernanceAccess(ctx, deps, u.ID)
		renderPageV2(w, r, deps, "Gouvernance", views.AccountGouvernancePage(rows))
	}
}

// handleAccountGouvernancePV rend le registre des délibérations et le procès-verbal
// d'une assemblée (lecture seule). Réservé au détenteur du grade de gouvernance.
func handleAccountGouvernancePV(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/gouvernance", http.StatusFound)
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()
		store := &governance.Store{DB: deps.DB}
		asm, err := store.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, governance.ErrAssemblyNotFound) {
				http.Error(w, "Assemblée introuvable", http.StatusNotFound)
				return
			}
			deps.Logger.Error("account_gouvernance_pv_assembly", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		register, err := store.ListRegister(ctx, id)
		if err != nil {
			deps.Logger.Error("account_gouvernance_pv_register", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		var minutesBody string
		if m, err := store.GetMinutes(ctx, id); err == nil {
			minutesBody = m.Body
		} else if !errors.Is(err, governance.ErrMinutesIntrouvable) {
			deps.Logger.Error("account_gouvernance_pv_minutes", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		logAccountGovernanceAccess(ctx, deps, u.ID)
		renderPageV2(w, r, deps, "Procès-verbal",
			views.AccountGouvernancePVPage(asm, register, minutesBody))
	}
}

// logAccountGovernanceAccess journalise l'accès d'un membre gouvernance à la surface
// de consultation (acteur et sujet identiques). N'échoue jamais la requête observée.
func logAccountGovernanceAccess(ctx context.Context, deps app.AppDeps, userID string) {
	gdpr.LogAccess(ctx, &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      userID,
		SubjectKind: gdpr.SubjectUser,
		SubjectID:   userID,
		ActorID:     userID,
		Action:      gdpr.ActionView,
	})
}

func governanceScheduledLabel(a governance.Assembly) string {
	if a.ScheduledAt == "" {
		return "(non datée)"
	}
	return a.ScheduledAt
}

func governanceStatusLabel(status string) string {
	switch status {
	case "drafting":
		return "Brouillon"
	case "convoked":
		return "Convoquée"
	case "cancelled":
		return "Annulée"
	default:
		return status
	}
}
