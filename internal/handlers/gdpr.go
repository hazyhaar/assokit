// CLAUDE:SUMMARY Handlers couche RGPD transverse : export des données du membre courant (/account/data-download, JSON) + page publique de politique de données (/donnees-personnelles, rendue depuis les const Go).
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// handleDataDownload restitue en JSON les données nominatives du membre courant
// (portabilité RGPD). Gating requireAuth au niveau route ; la défense u == nil
// reste en ceinture-et-bretelles. L'export est journalisé (action export).
func handleDataDownload(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/account/data-download", http.StatusFound)
			return
		}
		data, err := seeds.ExportMemberData(r.Context(), deps, u.ID)
		if err != nil {
			deps.Logger.Error("data_download", "user_id", u.ID, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
			UserID:      u.ID,
			SubjectKind: gdpr.SubjectUser,
			SubjectID:   u.ID,
			ActorID:     u.ID,
			Action:      gdpr.ActionExport,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="mes-donnees.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(data)
	}
}

// handleDataPolicy rend la page publique de politique de données, depuis le
// catalogue de conservation (const Go). Aucune table de configuration : la source
// unique est gdpr.RetentionCatalog.
func handleDataPolicy(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPageV2(w, r, deps, "Données personnelles", views.DataPolicyPage(gdpr.RetentionCatalog))
	}
}
