// CLAUDE:SUMMARY MountAdminConnectorsRoutes — wire les 4 routes /admin/connectors* sur le routeur chi.
// Override humain architect-5-2 2026-05-02 (mission 019de9fa). Sans ce wiring, handlers admin_connectors.go
// existaient mais étaient inaccessibles → Boris bloqué pour configurer HelloAsso.
package handlers

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// MountAdminConnectorsRoutes câble :
//   - GET  /admin/connectors                       → page HTML liste (admin)
//   - GET  /admin/connectors/{id}/configure        → page HTML configure (templ ConnectorsConfigurePage)
//   - GET  /admin/connectors/{id}/schema           → JSON schema (admin)
//   - POST /admin/connectors/{id}/configure        → soumission JSON values (admin)
//
// reg/life/vault peuvent être nil si ASSOKIT_MASTER_KEY absent → 503 explicite côté handlers.
func MountAdminConnectorsRoutes(r chi.Router, deps app.AppDeps, reg *connectors.Registry, life *connectors.Lifecycle, vault *assets.Vault) {
	if reg == nil {
		r.Get("/admin/connectors", connectorsDisabledHandler(deps))
		r.Get("/admin/connectors/{id}/configure", connectorsDisabledHandler(deps))
		r.Get("/admin/connectors/{id}/schema", connectorsDisabledHandler(deps))
		r.Post("/admin/connectors/{id}/configure", connectorsDisabledHandler(deps))
		return
	}
	r.Get("/admin/connectors", AdminConnectorsListHTML(deps, reg, life))
	r.Get("/admin/connectors/{id}/configure", AdminConnectorConfigurePage(deps, reg))
	r.Get("/admin/connectors/{id}/schema", AdminConnectorSchema(deps, reg))
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, vault))
}

func connectorsDisabledHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusServiceUnavailable)
		renderPageV2(w, r, deps, "Connecteurs désactivés", views.ConnectorsDisabledPage())
	}
}

// AdminConnectorsListHTML : page liste des connecteurs (admin).
func AdminConnectorsListHTML(deps app.AppDeps, reg *connectors.Registry, life *connectors.Lifecycle) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}
		items := make([]views.ConnectorListItem, 0, len(reg.All()))
		for _, c := range reg.All() {
			item := views.ConnectorListItem{
				ID:          c.ID(),
				DisplayName: c.DisplayName(),
				Description: c.Description(),
				Status:      connectorStatus(deps.DB, c.ID()),
			}
			if life != nil {
				h := life.Health(c.ID())
				item.HealthOK = h.OK
				item.HealthMsg = h.Message
			}
			items = append(items, item)
		}
		renderPageV2(w, r, deps, "Services externes", views.ConnectorsListPage(items))
	}
}

// AdminConnectorConfigurePage : page HTML de configuration d'un connecteur.
func AdminConnectorConfigurePage(deps app.AppDeps, reg *connectors.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}
		id := chi.URLParam(r, "id")
		c, ok := reg.Get(id)
		if !ok {
			http.Error(w, "connecteur inconnu", http.StatusNotFound)
			return
		}
		renderPageV2(w, r, deps, "Configurer "+c.DisplayName(), views.ConnectorsConfigurePage(c.ID(), c.DisplayName()))
	}
}
