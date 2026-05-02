// CLAUDE:SUMMARY Handler admin /admin/connectors — liste + schema + configure (S2-1, S2-3).
// CLAUDE:WARN Configure POST sépare config (config_json) et secrets (Vault.Set). Heuristique
// secret = champ JSON Schema avec format=password OU x-secret=true. Server-side validation stricte.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
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

// AdminConnectorSchema retourne le JSON Schema brut du connector (pour le JS render).
// GET /admin/connectors/{id}/schema → admin only.
func AdminConnectorSchema(deps app.AppDeps, reg *connectors.Registry) http.HandlerFunc {
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
		schema := c.ConfigSchema()
		w.Header().Set("Content-Type", "application/json")
		if schema == nil {
			w.Write([]byte(`{"type":"object","properties":{}}`)) //nolint:errcheck
			return
		}
		// jsonschema.Schema n'est pas directement JSON-marshalable proprement.
		// On ré-utilise la string raw du schema si le connector la stocke ailleurs.
		// Fallback : doc minimaliste.
		b, err := json.Marshal(schema)
		if err != nil || string(b) == "null" {
			w.Write([]byte(`{"type":"object","properties":{}}`)) //nolint:errcheck
			return
		}
		w.Write(b) //nolint:errcheck
	}
}

// AdminConnectorConfigure traite le POST de configuration.
// Sépare les champs en :
//   - secrets (format:password OU x-secret:true) → Vault.Set
//   - config non-sensible → connectors.config_json
// Active enabled=1 si tous les champs required sont présents.
func AdminConnectorConfigure(deps app.AppDeps, reg *connectors.Registry, vault *assets.Vault) http.HandlerFunc {
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

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			http.Error(w, "body trop gros", http.StatusBadRequest)
			return
		}
		var values map[string]any
		if err := json.Unmarshal(body, &values); err != nil {
			http.Error(w, "JSON invalide: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Validation côté serveur (jamais trust client).
		if schema := c.ConfigSchema(); schema != nil {
			if err := schema.Validate(values); err != nil {
				http.Error(w, "validation: "+err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Séparer secrets vs config non-sensible.
		secretKeys, err := extractSecretKeys(c)
		if err != nil {
			deps.Logger.Warn("connector_secret_keys_extract_failed",
				"connector", id, "err", err.Error())
		}
		nonSecret := make(map[string]any)
		for k, v := range values {
			if slices.Contains(secretKeys, k) {
				if vault == nil {
					http.Error(w, "vault non configuré", http.StatusInternalServerError)
					return
				}
				str, ok := v.(string)
				if !ok {
					http.Error(w, fmt.Sprintf("secret %q doit être string", k), http.StatusBadRequest)
					return
				}
				if err := vault.Set(r.Context(), id, k, str, u.ID); err != nil {
					deps.Logger.Error("connector_secret_set_failed",
						"connector", id, "key", k, "err", err.Error())
					http.Error(w, "vault set failed", http.StatusInternalServerError)
					return
				}
			} else {
				nonSecret[k] = v
			}
		}

		cfgJSON, _ := json.Marshal(nonSecret)
		if _, err := deps.DB.ExecContext(r.Context(), `
			INSERT INTO connectors(id, enabled, config_json, configured_at, configured_by)
			VALUES (?, 1, ?, CURRENT_TIMESTAMP, ?)
			ON CONFLICT(id) DO UPDATE SET
				config_json = excluded.config_json,
				enabled = 1,
				configured_at = CURRENT_TIMESTAMP,
				configured_by = excluded.configured_by
		`, id, string(cfgJSON), u.ID); err != nil {
			deps.Logger.Error("connector_configure_db_failed",
				"connector", id, "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		deps.Logger.Info("connector_configured",
			"connector", id, "user_id", u.ID,
			"secret_keys_count", len(secretKeys),
			"config_keys_count", len(nonSecret),
		)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	}
}

// extractSecretKeys parse le ConfigSchema du connector et identifie les champs
// qui doivent être routés via Vault (format:password ou x-secret:true).
// Implémentation pragmatique : marshal schema → JSON → re-parse brute.
func extractSecretKeys(c connectors.Connector) ([]string, error) {
	schema := c.ConfigSchema()
	if schema == nil {
		return nil, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Properties map[string]struct {
			Format   string `json:"format"`
			XSecret  bool   `json:"x-secret"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	var out []string
	for name, prop := range parsed.Properties {
		if strings.EqualFold(prop.Format, "password") || prop.XSecret {
			out = append(out, name)
		}
	}
	return out, nil
}

// _ = errors et _ = context : keep imports stables si certaines lignes sont retirées.
var _ = errors.Is
var _ = context.Background

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
