// CLAUDE:SUMMARY Handlers de l'espace propriétaire membre (V3a) : accueil consolidé
// (/account), cotisations (/account/cotisations), attestation imprimable
// (/account/attestation), déclaration de mutation (/account/mutation GET+POST).
// Toutes les vues sont rôle-scopées sur le membre connecté (requireAuth) ; aucune
// donnée fabriquée : tout provient des stores réels (membership/parcelle/convention/
// identity/mutation), état vide assumé sinon.
package handlers

import (
	"net/http"
	"slices"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/mutation"
	"github.com/hazyhaar/assokit/pkg/parcelle"
	"github.com/hazyhaar/assokit/pkg/profilrequest"
)

// membershipStateLabel traduit le statut d'adhésion courante (CurrentStatus) en
// libellé lisible. "none" signifie qu'aucune adhésion active ne couvre le jour.
func membershipStateLabel(status string) string {
	switch status {
	case "active":
		return "à jour"
	case "none":
		return "aucune adhésion active"
	default:
		return status
	}
}

// handleAccountHome rend l'accueil consolidé du propriétaire : compteurs de
// parcelles/conventions/cotisations et état d'adhésion, agrégés depuis les stores
// réels du membre connecté. Aucune vue détaillée n'est recodée : résumés + liens.
func handleAccountHome(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account", http.StatusFound)
			return
		}
		ctx := r.Context()

		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_home_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		convs, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_home_conventions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		cotis, err := (&membership.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_home_cotisations", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		state, err := (&membership.Store{DB: deps.DB}).CurrentStatus(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_home_state", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		var surface int64
		for _, p := range parcs {
			surface += p.SurfaceM2
		}
		summary := views.AccountSummary{
			DisplayName:     u.DisplayName,
			ParcellesCount:  len(parcs),
			SurfaceTotalM2:  surface,
			ConventionsLen:  len(convs),
			CotisationsLen:  len(cotis),
			MembershipState: membershipStateLabel(state),
		}
		tabs := metierTabViews(visibleMetierTabs(deps.MetierTabs, u.Roles), "/account")
		cards := filterAccountCards(views.BuildAccountHomeCards(summary), deps.MetierTabs, u.Roles)
		cards = filterDisabledModuleCards(cards, deps.DisabledModules)
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Espace propriétaire", views.AccountHomePage(summary, tabs, cards))
	}
}

// handleAccountProfils liste les profils métier détenus, les grades requestables
// non détenus et l'état des demandes en cours du membre.
func handleAccountProfils(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/profils", http.StatusFound)
			return
		}
		ctx := r.Context()
		visible := visibleMetierTabs(deps.MetierTabs, u.Roles)
		profils := make([]views.MetierProfilView, 0, len(visible))
		for _, tab := range visible {
			profils = append(profils, views.MetierProfilView{Label: tab.Label})
		}
		requestable := make([]views.ProfileRequestableView, 0)
		for _, g := range deps.ProfileGrant.Requestable {
			if !slices.Contains(u.Roles, g.Name) {
				requestable = append(requestable, views.ProfileRequestableView{
					GradeID: g.ID,
					Label:   g.Name,
				})
			}
		}
		pendingViews := make([]views.ProfileRequestStatusView, 0)
		if len(deps.ProfileGrant.Requestable) > 0 {
			reqs, err := (&profilrequest.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
			if err != nil {
				deps.Logger.Error("account_profils_requests", "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
			for _, req := range reqs {
				if req.Statut != "soumise" {
					continue
				}
				label := gradeNameForID(deps.ProfileGrant, req.GradeID)
				if label == "" {
					label = req.GradeID
				}
				pendingViews = append(pendingViews, views.ProfileRequestStatusView{
					GradeLabel: label,
					Statut:     req.Statut,
				})
			}
		}
		csrfToken := middleware.CSRFToken(ctx)
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Mes profils",
			views.AccountProfilsPage(profils, requestable, pendingViews, csrfToken))
	}
}

// handleAccountCotisations affiche les appels de cotisation du membre courant
// (lecture seule), via membership.Store.ListForUser.
func handleAccountCotisations(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/cotisations", http.StatusFound)
			return
		}
		list, err := (&membership.Store{DB: deps.DB}).ListForUser(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("account_cotisations", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Mes cotisations", views.AccountCotisationsPage(list))
	}
}

// handleAccountAttestation génère un document imprimable d'attestation d'adhésion
// pour le membre courant, sur le patron du document imprimable des conventions (C4 :
// article HTML rendu via renderPageV2, impression navigateur = PDF). Le contenu est
// réel : identité du membre (identity.Store), état d'adhésion (CurrentStatus) et
// parcelles rattachées (parcelle.ListForUser). Aucune donnée fabriquée.
func handleAccountAttestation(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/attestation", http.StatusFound)
			return
		}
		ctx := r.Context()

		member, err := (&identity.Store{DB: deps.DB}).GetByID(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_attestation_member", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		if member == nil {
			http.NotFound(w, r)
			return
		}
		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_attestation_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		state, err := (&membership.Store{DB: deps.DB}).CurrentStatus(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_attestation_state", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Attestation d'adhésion",
			views.AccountAttestationPage(member.DisplayName, member.Email, membershipStateLabel(state), parcs))
	}
}

// handleAccountMutation gère GET (formulaire + déclarations existantes) et POST
// (enregistrement d'une déclaration) sur /account/mutation. Le propriétaire déclare
// une mutation portant sur une de ses parcelles ; la déclaration est une trace
// consultable côté administration (mutation.Store), sans transfert automatique. CSRF
// assuré par le middleware global (champ _csrf du formulaire).
func handleAccountMutation(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/mutation", http.StatusFound)
			return
		}
		ctx := r.Context()
		store := &mutation.Store{DB: deps.DB}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			_, err := store.Create(ctx, mutation.Declaration{
				UserID:      u.ID,
				ParcelleRef: strings.TrimSpace(r.FormValue("parcelle_ref")),
				Type:        strings.TrimSpace(r.FormValue("type")),
				Details:     strings.TrimSpace(r.FormValue("details")),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Déclaration de mutation enregistrée.")
			}
			http.Redirect(w, r, "/account/mutation", http.StatusSeeOther)
			return
		}

		parcs, err := (&parcelle.Store{DB: deps.DB}).ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_mutation_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		decls, err := store.ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_mutation_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(ctx)
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Déclarer une mutation",
			views.AccountMutationPage(parcs, decls, csrfToken))
	}
}

// handleAdminMutations affiche la liste de toutes les déclarations de mutation
// (vue administration), via mutation.Store.ListAll. Gating admin assuré par
// requireAdmin sur la route.
func handleAdminMutations(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := (&mutation.Store{DB: deps.DB}).ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_mutations", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Déclarations de mutation", views.AdminMutationsPage(list))
	}
}

// logAccountSelfAccess journalise un accès du membre à ses propres données (acteur
// et sujet identiques), au titre de la redevabilité d'accès. N'échoue jamais la
// requête observée (warn-loud via gdpr.LogAccess).
func logAccountSelfAccess(r *http.Request, deps app.AppDeps, userID string) {
	gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      userID,
		SubjectKind: gdpr.SubjectUser,
		SubjectID:   userID,
		ActorID:     userID,
		Action:      gdpr.ActionView,
	})
}
