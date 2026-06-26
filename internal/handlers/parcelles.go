// CLAUDE:SUMMARY Handlers UI registre parcellaire AFP : admin (liste + création + droit) et vue membre filtrée par appartenance.
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// handleAdminParcelles gère GET (liste + formulaire) et POST (création d'une
// parcelle) sur /admin/parcelles. Gating admin assuré par requireAdmin sur la
// route.
func handleAdminParcelles(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &parcelle.Store{DB: deps.DB}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			surface, convErr := strconv.ParseInt(strings.TrimSpace(r.FormValue("surface_m2")), 10, 64)
			if convErr != nil || surface < 0 {
				middleware.PushFlash(w, "error", "Surface invalide : indiquer un entier de mètres carrés positif.")
				http.Redirect(w, r, "/admin/parcelles", http.StatusSeeOther)
				return
			}
			_, err := store.Create(r.Context(), parcelle.Parcelle{
				CommuneCode:    r.FormValue("commune_code"),
				Section:        r.FormValue("section"),
				NumeroParcelle: r.FormValue("numero_parcelle"),
				SurfaceM2:      surface,
				StatutMad:      r.FormValue("statut_mad"),
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
			} else {
				middleware.PushFlash(w, "success", "Parcelle enregistrée.")
			}
			http.Redirect(w, r, "/admin/parcelles", http.StatusSeeOther)
			return
		}

		list, err := store.ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Consultation administrative du registre parcellaire : accès aux données
		// nominatives de l'ensemble des parcelles (occupants/propriétaires). Le
		// sujet est le registre lui-même, l'acteur l'administrateur courant.
		logCadastreAccess(r, deps, gdpr.SubjectParcelle, "all", gdpr.ActionView)
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Registre parcellaire", views.ParcelsAdminPage(list, csrfToken))
	}
}

// handleAdminParcelleDroit traite POST /admin/parcelles/droit : attache un droit
// (membre + nature) à une parcelle. Gating admin via requireAdmin sur la route.
func handleAdminParcelleDroit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}
		store := &parcelle.Store{DB: deps.DB}
		droitUserID := r.FormValue("user_id")
		id, err := store.AddDroit(r.Context(),
			r.FormValue("parcelle_id"), droitUserID, r.FormValue("nature_du_droit"))
		if err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Droit attaché.")
			// Création d'un droit nominatif : un membre est rattaché à une
			// parcelle. Le sujet est l'utilisateur concerné par le droit.
			e := gdpr.AccessLog{
				UserID:      droitUserID,
				SubjectKind: gdpr.SubjectDroit,
				SubjectID:   id,
				Action:      gdpr.ActionView,
			}
			if u := middleware.UserFromContext(r.Context()); u != nil {
				e.ActorID = u.ID
			}
			gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, e)
		}
		http.Redirect(w, r, "/admin/parcelles", http.StatusSeeOther)
	}
}

// handleMyParcelles affiche les parcelles sur lesquelles l'utilisateur courant
// détient un droit. Le filtrage d'appartenance est appliqué dans la requête
// (ListForUser : clause WHERE parcelle_droits.user_id = utilisateur courant).
func handleMyParcelles(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/parcelles", http.StatusFound)
			return
		}
		store := &parcelle.Store{DB: deps.DB}
		list, err := store.ListForUser(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("my_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Le membre consulte ses propres parcelles : acteur et sujet sont le même
		// utilisateur. Journalisé au titre de la redevabilité d'accès.
		gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
			UserID:      u.ID,
			SubjectKind: gdpr.SubjectUser,
			SubjectID:   u.ID,
			ActorID:     u.ID,
			Action:      gdpr.ActionView,
		})
		renderPageV2(w, r, deps, "Mes parcelles", views.MyParcelsPage(list))
	}
}

// logCadastreAccess journalise un accès nominatif au registre parcellaire. L'acteur
// est l'utilisateur courant ; un accès anonyme (acteur absent) n'est pas
// journalisé (les routes cadastre sont déjà gardées en amont). N'échoue jamais la
// requête observée (warn-loud via gdpr.LogAccess).
func logCadastreAccess(r *http.Request, deps app.AppDeps, subjectKind, subjectID, action string) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		return
	}
	gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		SubjectKind: subjectKind,
		SubjectID:   subjectID,
		ActorID:     u.ID,
		Action:      action,
	})
}
