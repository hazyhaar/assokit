// CLAUDE:SUMMARY Parcours O2 : demande d'octroi de profil métier (membre),
// octroi et retrait (gouvernance). Contrôles serveur sur la liste blanche
// ProfileGrant ; octroi atomique (statut + grade) ; audit gdpr distinct du RBAC.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/profilrequest"
)

// errProfilMetier est une erreur métier explicite (refus de demande ou d'octroi).
type errProfilMetier string

func (e errProfilMetier) Error() string { return string(e) }

const (
	errGradeNonRequestable  errProfilMetier = "Ce profil n'est pas demandable sur cette instance."
	errGradeDejaDetenu      errProfilMetier = "Vous détenez déjà ce profil."
	errDemandeDejaSoumise   errProfilMetier = "Une demande est déjà en cours pour ce profil."
	errOctroiGouvernance    errProfilMetier = "Le grade de gouvernance ne peut pas être octroyé."
	errAutoOctroi           errProfilMetier = "L'auto-octroi est interdit."
	errDemandeIntrouvable   errProfilMetier = "Demande introuvable."
	errDemandeNonSoumise    errProfilMetier = "Cette demande n'est plus en attente."
	errRBACIndisponible     errProfilMetier = "Service d'autorisation indisponible."
	errRetraitGradeInterdit errProfilMetier = "Ce grade ne peut pas être retiré via ce parcours."
)

// grantableByID retourne le grade requestable correspondant à un identifiant, ou nil.
func grantableByID(grant app.ProfileGrant, gradeID string) *app.GrantableGrade {
	for i := range grant.Requestable {
		if grant.Requestable[i].ID == gradeID {
			return &grant.Requestable[i]
		}
	}
	return nil
}

// isRequestable vérifie qu'un identifiant de grade figure dans la liste blanche.
func isRequestable(grant app.ProfileGrant, gradeID string) bool {
	return grantableByID(grant, gradeID) != nil
}

// gradeNameForID résout le nom RBAC (projection u.Roles) depuis l'identifiant.
func gradeNameForID(grant app.ProfileGrant, gradeID string) string {
	if g := grantableByID(grant, gradeID); g != nil {
		return g.Name
	}
	return ""
}

// handleAccountProfilsDemande enregistre une demande d'octroi pour un grade
// requestable. Refuse si le grade est déjà détenu ou si une demande soumise existe.
func handleAccountProfilsDemande(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/profils", http.StatusFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		gradeID := strings.TrimSpace(r.FormValue("grade_id"))
		ctx := r.Context()
		store := &profilrequest.Store{DB: deps.DB}

		if err := validateDemande(ctx, deps.ProfileGrant, u, gradeID, store); err != nil {
			middleware.PushFlash(w, "error", err.Error())
			http.Redirect(w, r, "/account/profils", http.StatusSeeOther)
			return
		}
		if _, err := store.Create(ctx, u.ID, gradeID); err != nil {
			deps.Logger.Error("profil_demande_create", "err", err.Error())
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Demande de profil enregistrée.")
		}
		http.Redirect(w, r, "/account/profils", http.StatusSeeOther)
	}
}

func validateDemande(ctx context.Context, grant app.ProfileGrant, u *identity.User, gradeID string, store *profilrequest.Store) error {
	if !isRequestable(grant, gradeID) {
		return errGradeNonRequestable
	}
	gradeName := gradeNameForID(grant, gradeID)
	if gradeName != "" && slices.Contains(u.Roles, gradeName) {
		return errGradeDejaDetenu
	}
	exists, err := store.ExistsSoumise(ctx, u.ID, gradeID)
	if err != nil {
		return fmt.Errorf("vérification demande: %w", err)
	}
	if exists {
		return errDemandeDejaSoumise
	}
	return nil
}

// handleAccountProfilsOctroi liste les demandes soumises (GET) et octroie un grade
// (POST) après contrôles serveur. Réservé au détenteur du grade de gouvernance.
func handleAccountProfilsOctroi(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/profils/octroi", http.StatusFound)
			return
		}
		ctx := r.Context()
		store := &profilrequest.Store{DB: deps.DB}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			reqID := strings.TrimSpace(r.FormValue("request_id"))
			if err := octroyerProfil(ctx, deps, u, store, reqID); err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Profil octroyé.")
			}
			http.Redirect(w, r, "/account/profils/octroi", http.StatusSeeOther)
			return
		}

		pending, err := store.ListByStatut(ctx, "soumise")
		if err != nil {
			deps.Logger.Error("profil_octroi_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		viewsPending := make([]views.ProfileRequestPendingView, 0, len(pending))
		idStore := &identity.Store{DB: deps.DB}
		for _, req := range pending {
			v := views.ProfileRequestPendingView{
				ID:      req.ID,
				GradeID: req.GradeID,
			}
			if g := grantableByID(deps.ProfileGrant, req.GradeID); g != nil {
				v.GradeLabel = g.Name
			}
			if member, _ := idStore.GetByID(ctx, req.UserID); member != nil {
				v.UserLabel = member.DisplayName
				if v.UserLabel == "" {
					v.UserLabel = member.Email
				}
			} else {
				v.UserLabel = req.UserID
			}
			viewsPending = append(viewsPending, v)
		}
		csrfToken := middleware.CSRFToken(ctx)
		renderPageV2(w, r, deps, "Octroi de profils",
			views.AccountProfilsOctroiPage(viewsPending, csrfToken))
	}
}

func octroyerProfil(ctx context.Context, deps app.AppDeps, actor *identity.User, store *profilrequest.Store, reqID string) error {
	if deps.RBAC == nil {
		return errRBACIndisponible
	}
	req, err := store.GetByID(ctx, reqID)
	if err != nil {
		return fmt.Errorf("lecture demande: %w", err)
	}
	if req == nil {
		return errDemandeIntrouvable
	}
	if req.Statut != "soumise" {
		return errDemandeNonSoumise
	}
	gradeID := req.GradeID
	grant := deps.ProfileGrant
	if !isRequestable(grant, gradeID) {
		return errGradeNonRequestable
	}
	if gradeID == grant.GovernanceGradeID {
		return errOctroiGouvernance
	}
	if req.UserID == actor.ID {
		return errAutoOctroi
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("octroi begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := store.SetStatutTx(ctx, tx, req.ID, "acceptee"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES(?, ?)`,
		req.UserID, gradeID,
	); err != nil {
		return fmt.Errorf("octroi assign grade: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("octroi commit: %w", err)
	}
	if err := deps.RBAC.Recompute(ctx, req.UserID); err != nil {
		return fmt.Errorf("octroi recompute: %w", err)
	}

	logProfilGovernanceAccess(ctx, deps, actor.ID, req.UserID, "grant_grade")
	return nil
}

// handleAccountProfilsRetrait retire un grade métier requestable d'un membre.
// Ne supprime aucun artefact métier (parcelles, conventions, questions).
func handleAccountProfilsRetrait(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/profils/octroi", http.StatusFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		targetUserID := strings.TrimSpace(r.FormValue("user_id"))
		gradeID := strings.TrimSpace(r.FormValue("grade_id"))
		grant := deps.ProfileGrant

		if !isRequestable(grant, gradeID) {
			middleware.PushFlash(w, "error", errGradeNonRequestable.Error())
			http.Redirect(w, r, "/account/profils/octroi", http.StatusSeeOther)
			return
		}
		if gradeID == grant.GovernanceGradeID {
			middleware.PushFlash(w, "error", errRetraitGradeInterdit.Error())
			http.Redirect(w, r, "/account/profils/octroi", http.StatusSeeOther)
			return
		}
		if deps.RBAC == nil {
			middleware.PushFlash(w, "error", errRBACIndisponible.Error())
			http.Redirect(w, r, "/account/profils/octroi", http.StatusSeeOther)
			return
		}
		ctx := r.Context()
		if err := deps.RBAC.RemoveGrade(ctx, targetUserID, gradeID); err != nil {
			deps.Logger.Error("profil_retrait", "err", err.Error())
			middleware.PushFlash(w, "error", err.Error())
		} else {
			logProfilGovernanceAccess(ctx, deps, u.ID, targetUserID, "revoke_grade")
			middleware.PushFlash(w, "success", "Grade retiré.")
		}
		http.Redirect(w, r, "/account/profils/octroi", http.StatusSeeOther)
	}
}

// logProfilGovernanceAccess journalise un octroi ou retrait : acteur = gouvernance,
// sujet = bénéficiaire ou cible du retrait.
func logProfilGovernanceAccess(ctx context.Context, deps app.AppDeps, actorID, subjectID, action string) {
	gdpr.LogAccess(ctx, &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      subjectID,
		SubjectKind: gdpr.SubjectUser,
		SubjectID:   subjectID,
		ActorID:     actorID,
		Action:      action,
	})
}
