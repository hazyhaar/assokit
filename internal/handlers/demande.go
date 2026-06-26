// CLAUDE:SUMMARY Parcours guidés de demande de mise à disposition (V3c) : membre
// preneur (/account/demande-mad GET+POST, rôle-scopé userID session) et conversion
// administrative en convention (/admin/demandes liste, /admin/demandes/{id}/convertir
// GET formulaire pré-rempli + POST cascade demande→convention). La conversion appelle
// convention.Store.Create (réutilisé, non modifié) puis passe la demande à
// « convertie » via demande.Store.SetStatut (garde d'état : une demande terminale ne
// se reconvertit pas). Aucune donnée fabriquée : tout provient des stores réels.
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/demande"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// handleAccountDemandeMad gère GET (formulaire + demandes déjà soumises) et POST
// (soumission) sur /account/demande-mad. La demande est rôle-scopée sur le membre
// connecté (userID de session) ; le statut initial « soumise » est forcé serveur.
// CSRF assuré par le middleware global (champ _csrf du formulaire).
func handleAccountDemandeMad(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/demande-mad", http.StatusFound)
			return
		}
		ctx := r.Context()
		store := &demande.Store{DB: deps.DB}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			_, err := store.Create(ctx, demande.Demande{
				UserID:      u.ID, // userID de session : jamais une valeur du formulaire.
				ParcelleRef: strings.TrimSpace(r.FormValue("parcelle_ref")),
				Objet:       strings.TrimSpace(r.FormValue("objet")),
				Details:     strings.TrimSpace(r.FormValue("details")),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Demande de mise à disposition soumise.")
			}
			http.Redirect(w, r, "/account/demande-mad", http.StatusSeeOther)
			return
		}

		// Liste de parcelles du registre pour suggestion (datalist), sans fuite : ce
		// sont des références cadastrales publiques au sein de l'association.
		parcs, err := (&parcelle.Store{DB: deps.DB}).ListAll(ctx)
		if err != nil {
			deps.Logger.Error("account_demande_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		demandes, err := store.ListForUser(ctx, u.ID)
		if err != nil {
			deps.Logger.Error("account_demande_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(ctx)
		logAccountSelfAccess(r, deps, u.ID)
		renderPageV2(w, r, deps, "Demander une mise à disposition",
			views.DemandeMadPage(parcs, demandes, csrfToken))
	}
}

// handleAdminDemandes affiche la liste de toutes les demandes de mise à disposition
// (vue administration), via demande.Store.ListAll. Gating admin via requireAdmin.
func handleAdminDemandes(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := (&demande.Store{DB: deps.DB}).ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_demandes", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Demandes de mise à disposition", views.AdminDemandesPage(list))
	}
}

// handleAdminDemandeConvertir gère GET (formulaire de convention pré-rempli depuis la
// demande) et POST (cascade : convention.Store.Create puis demande→convertie). Une
// demande déjà convertie ou refusée ne se reconvertit pas (garde d'état dans
// demande.Store.SetStatut). Le POST accepte aussi un refus explicite (action=refuser).
func handleAdminDemandeConvertir(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		store := &demande.Store{DB: deps.DB}
		d, err := store.GetByID(r.Context(), id)
		if err != nil {
			if err == demande.ErrNotFound {
				http.NotFound(w, r)
				return
			}
			deps.Logger.Error("admin_demande_convertir_get", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			// Garde d'état en amont : une demande terminale ne se retravaille pas.
			if d.Statut == demande.StatutConvertie || d.Statut == demande.StatutRefusee {
				middleware.PushFlash(w, "error", "Cette demande est déjà close (convertie ou refusée) : elle ne peut être retravaillée.")
				http.Redirect(w, r, "/admin/demandes", http.StatusSeeOther)
				return
			}
			if strings.TrimSpace(r.FormValue("action")) == "refuser" {
				if err := store.SetStatut(r.Context(), id, demande.StatutRefusee); err != nil {
					middleware.PushFlash(w, "error", err.Error())
				} else {
					middleware.PushFlash(w, "success", "Demande refusée.")
				}
				http.Redirect(w, r, "/admin/demandes", http.StatusSeeOther)
				return
			}

			duree, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("duree_mois")))
			if convErr != nil || duree <= 0 {
				duree = convention.DureeMoisDefaut
			}
			// Le preneur est ancré sur l'auteur de la demande chargée (d.UserID),
			// jamais sur un preneur_id du formulaire : un POST forgé ne peut pas
			// créer une convention au nom d'un autre membre.
			convID, err := (&convention.Store{DB: deps.DB}).Create(r.Context(), convention.Convention{
				PreneurID: d.UserID,
				Parcelles: r.Form["parcelle_id"],
				DureeMois: duree,
			})
			if err != nil {
				middleware.PushFlash(w, "error", "Création de la convention : "+err.Error())
				http.Redirect(w, r, "/admin/demandes/"+id+"/convertir", http.StatusSeeOther)
				return
			}
			if err := store.SetStatut(r.Context(), id, demande.StatutConvertie); err != nil {
				// La convention est créée mais la demande n'a pu être marquée : fail-loud,
				// l'administrateur doit savoir que la demande reste ouverte.
				middleware.PushFlash(w, "error", "Convention créée mais la demande n'a pu être marquée convertie : "+err.Error())
				http.Redirect(w, r, "/admin/demandes", http.StatusSeeOther)
				return
			}
			logConventionAccess(r, deps, gdpr.SubjectUser, d.UserID, gdpr.ActionView)
			middleware.PushFlash(w, "success", "Demande convertie en convention.")
			http.Redirect(w, r, "/admin/conventions/"+convID+"/genere", http.StatusSeeOther)
			return
		}

		if d.Statut == demande.StatutConvertie || d.Statut == demande.StatutRefusee {
			middleware.PushFlash(w, "error", "Cette demande est déjà close (convertie ou refusée).")
			http.Redirect(w, r, "/admin/demandes", http.StatusSeeOther)
			return
		}

		// Formulaire pré-rempli : preneur = demandeur, parcelles candidates = registre
		// (la référence souhaitée de la demande est rappelée pour guider la sélection).
		parcs, err := (&parcelle.Store{DB: deps.DB}).ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_demande_convertir_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Convertir une demande en convention",
			views.DemandeConvertirPage(d, parcs, csrfToken))
	}
}
