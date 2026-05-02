// CLAUDE:SUMMARY Handler admin /admin/connectors — liste connectors + status, stub Configurer/Tester (M-ASSOKIT-SPRINT2-S1).
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// AdminConnectorsView : ligne UI pour un connector listé.
type AdminConnectorsView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Status      string `json:"status"` // "not_configured" | "disabled" | "running" | "error"
	HealthOK    bool   `json:"health_ok"`
	HealthMsg   string `json:"health_msg"`
}

// AdminConnectorsList retourne la liste JSON des connectors avec status.
// GET /admin/connectors → admin only.
func AdminConnectorsList(deps app.AppDeps, reg *connectors.Registry, life *connectors.Lifecycle) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}

		views := make([]AdminConnectorsView, 0)
		for _, c := range reg.All() {
			view := AdminConnectorsView{
				ID:          c.ID(),
				DisplayName: c.DisplayName(),
				Description: c.Description(),
				Status:      connectorStatus(deps.DB, c.ID()),
			}
			if life != nil {
				h := life.Health(c.ID())
				view.HealthOK = h.OK
				view.HealthMsg = h.Message
			}
			views = append(views, view)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(views) //nolint:errcheck
	}
}

// connectorStatus interroge la DB pour déterminer le status courant.
func connectorStatus(db *sql.DB, id string) string {
	if db == nil {
		return "not_configured"
	}
	var enabled int
	var configuredAt sql.NullString
	err := db.QueryRow(
		`SELECT enabled, configured_at FROM connectors WHERE id = ?`, id,
	).Scan(&enabled, &configuredAt)
	if err == sql.ErrNoRows || !configuredAt.Valid {
		return "not_configured"
	}
	if err != nil {
		return "error"
	}
	if enabled == 0 {
		return "disabled"
	}
	return "running"
}
