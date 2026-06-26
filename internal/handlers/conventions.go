// CLAUDE:SUMMARY Handlers UI conventions de pâturage AFP : admin (liste + formulaire
// guidé + génération du document imprimable) et vue preneur filtrée par appartenance.
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// handleAdminConventions gère GET (liste + formulaire guidé) et POST (création)
// sur /admin/conventions. Gating admin assuré par requireAdmin sur la route. La
// création d'une convention rattache un preneur nominatif : l'accès est journalisé.
func handleAdminConventions(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &convention.Store{DB: deps.DB}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "formulaire invalide", http.StatusBadRequest)
				return
			}
			duree, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("duree_mois")))
			if convErr != nil || duree <= 0 {
				duree = convention.DureeMoisDefaut
			}
			id, err := store.Create(r.Context(), convention.Convention{
				PreneurID: r.FormValue("preneur_id"),
				Parcelles: r.Form["parcelle_id"],
				DureeMois: duree,
			})
			if err != nil {
				middleware.PushFlash(w, "error", err.Error())
				http.Redirect(w, r, "/admin/conventions", http.StatusSeeOther)
				return
			}
			// Création d'une convention liée à un membre preneur : accès nominatif.
			logConventionAccess(r, deps, gdpr.SubjectUser, r.FormValue("preneur_id"), gdpr.ActionView)
			middleware.PushFlash(w, "success", "Convention créée.")
			http.Redirect(w, r, "/admin/conventions/"+id+"/genere", http.StatusSeeOther)
			return
		}

		list, err := store.ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_conventions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		parcs, err := (&parcelle.Store{DB: deps.DB}).ListAll(r.Context())
		if err != nil {
			deps.Logger.Error("admin_conventions_parcelles", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Conventions de pâturage", views.ConventionsAdminPage(list, parcs, csrfToken))
	}
}

// handleAdminConventionGenere assemble et rend le document imprimable d'une
// convention via le fournisseur de clauses injecté. Le contenu des clauses est
// produit hors du core (provider de bordure) ; en l'absence d'adaptateur réel, le
// fournisseur par défaut est inerte et la convention s'affiche sans articles.
func handleAdminConventionGenere(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		conv, clauses, err := seeds.AssembleConvention(r.Context(), deps, id)
		if err != nil {
			if err == convention.ErrConventionNotFound {
				http.NotFound(w, r)
				return
			}
			deps.Logger.Error("admin_convention_genere", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Lecture d'une convention liée à un preneur nominatif.
		logConventionAccess(r, deps, gdpr.SubjectUser, conv.PreneurID, gdpr.ActionView)
		renderPageV2(w, r, deps, "Convention de pâturage", views.ConventionDocumentPage(conv, clauses))
	}
}

// handleAdminConventionRevoke traite POST /admin/conventions/{id}/revoke.
func handleAdminConventionRevoke(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := (&convention.Store{DB: deps.DB}).Revoke(r.Context(), id); err != nil {
			middleware.PushFlash(w, "error", err.Error())
		} else {
			middleware.PushFlash(w, "success", "Convention révoquée.")
		}
		http.Redirect(w, r, "/admin/conventions", http.StatusSeeOther)
	}
}

// handleMyConventions affiche les conventions dont l'utilisateur courant est
// preneur. Le filtrage d'appartenance est appliqué dans la requête (ListForUser).
func handleMyConventions(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/conventions", http.StatusFound)
			return
		}
		list, err := (&convention.Store{DB: deps.DB}).ListForUser(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("my_conventions", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Le membre consulte ses propres conventions : acteur et sujet identiques.
		gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
			UserID:      u.ID,
			SubjectKind: gdpr.SubjectUser,
			SubjectID:   u.ID,
			ActorID:     u.ID,
			Action:      gdpr.ActionView,
		})
		renderPageV2(w, r, deps, "Mes conventions", views.MyConventionsPage(list))
	}
}

// logConventionAccess journalise un accès nominatif lié à une convention. L'acteur
// est l'administrateur courant ; un accès anonyme n'est pas journalisé (route
// gardée en amont). N'échoue jamais la requête observée.
func logConventionAccess(r *http.Request, deps app.AppDeps, subjectKind, subjectID, action string) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		return
	}
	gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      subjectID,
		SubjectKind: subjectKind,
		SubjectID:   subjectID,
		ActorID:     u.ID,
		Action:      action,
	})
}
