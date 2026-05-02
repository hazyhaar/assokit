// CLAUDE:SUMMARY MountAdminConnectorsRoutes — wire les 4 routes /admin/connectors* sur le routeur chi.
// Override humain architect-5-2 2026-05-02 (mission 019de9fa). Sans ce wiring, handlers admin_connectors.go
// existaient mais étaient inaccessibles → Boris bloqué pour configurer HelloAsso.
package handlers

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/admin"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// MountAdminConnectorsRoutes câble :
//   - GET  /admin/connectors                       → page HTML liste (admin)
//   - GET  /admin/connectors/{id}/configure        → page HTML configure (templ ConnectorsConfigurePage)
//   - GET  /admin/connectors/{id}/schema           → JSON schema (admin)
//   - POST /admin/connectors/{id}/configure        → soumission JSON values (admin)
//
// reg/life/vault peuvent être nil si NPS_MASTER_KEY absent → 503 explicite côté handlers.
func MountAdminConnectorsRoutes(r chi.Router, deps app.AppDeps, reg *connectors.Registry, life *connectors.Lifecycle, vault *assets.Vault) {
	if reg == nil {
		r.Get("/admin/connectors", connectorsDisabledHandler)
		r.Get("/admin/connectors/{id}/configure", connectorsDisabledHandler)
		r.Get("/admin/connectors/{id}/schema", connectorsDisabledHandler)
		r.Post("/admin/connectors/{id}/configure", connectorsDisabledHandler)
		return
	}
	r.Get("/admin/connectors", AdminConnectorsListHTML(deps, reg, life))
	r.Get("/admin/connectors/{id}/configure", AdminConnectorConfigurePage(deps, reg))
	r.Get("/admin/connectors/{id}/schema", AdminConnectorSchema(deps, reg))
	r.Post("/admin/connectors/{id}/configure", AdminConnectorConfigure(deps, reg, vault))
}

func connectorsDisabledHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "3600")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Connectors désactivés</title><h1>Connectors désactivés</h1><p>NPS_MASTER_KEY non configurée — Vault indisponible. Contactez l'administrateur serveur.</p>`))
}

// AdminConnectorsListHTML : page liste minimale (HTML, pas JSON) — un lien par connector.
// Les non-admins reçoivent 403.
func AdminConnectorsListHTML(deps app.AppDeps, reg *connectors.Registry, life *connectors.Lifecycle) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8"><title>Connectors — Admin</title><style>body{font-family:system-ui,sans-serif;max-width:720px;margin:2rem auto;padding:0 1rem;}h1{color:#00897b}.c{border:1px solid #e2ddd6;padding:1rem;margin:.5rem 0;border-radius:2px}.c a{color:#00897b;text-decoration:none;font-weight:600}.c a:hover{text-decoration:underline}.s{font-size:.85rem;color:#6b6560;margin-top:.25rem}.ok{color:#2e7d32}.ko{color:#c62828}</style></head><body><h1>Connectors</h1>`))
		for _, c := range reg.All() {
			status := connectorStatus(deps.DB, c.ID())
			healthCls, healthMsg := "", ""
			if life != nil {
				h := life.Health(c.ID())
				if h.OK {
					healthCls = "ok"
					healthMsg = "OK"
				} else if h.Message != "" {
					healthCls = "ko"
					healthMsg = h.Message
				}
			}
			_, _ = w.Write([]byte(`<div class="c"><a href="/admin/connectors/` + c.ID() + `/configure">` + htmlEscapeMin(c.DisplayName()) + `</a><div class="s">` + htmlEscapeMin(c.Description()) + `</div><div class="s">Statut : <strong>` + status + `</strong>`))
			if healthMsg != "" {
				_, _ = w.Write([]byte(` — <span class="` + healthCls + `">` + htmlEscapeMin(healthMsg) + `</span>`))
			}
			_, _ = w.Write([]byte(`</div></div>`))
		}
		_, _ = w.Write([]byte(`</body></html>`))
	}
}

// AdminConnectorConfigurePage : page HTML wrap autour de templ ConnectorsConfigurePage.
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
			http.Error(w, "connector inconnu", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := admin.ConnectorsConfigurePage(c.ID(), c.DisplayName()).Render(r.Context(), w); err != nil {
			deps.Logger.Warn("admin_connector_configure_render", "err", err.Error())
		}
	}
}

func htmlEscapeMin(s string) string {
	rep := map[rune]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '"': "&quot;", '\'': "&#39;"}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if v, ok := rep[r]; ok {
			out = append(out, v...)
			continue
		}
		out = append(out, string(r)...)
	}
	return string(out)
}
