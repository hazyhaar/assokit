// CLAUDE:SUMMARY adminpanel handlers — Mount() câble GET /admin/panel, POST save-field, GET progress, upload, delete-file (M-ASSOKIT-ADMIN-PANEL-V0).
package adminpanel

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	adminui "github.com/hazyhaar/assokit/pkg/horui/admin"
	"github.com/hazyhaar/assokit/pkg/horui/branding"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// requireAdmin vérifie que l'utilisateur a le rôle "admin".
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Mount câble les routes /admin/panel sur le router fourni.
func Mount(r chi.Router, deps app.AppDeps) {
	fields := V0Fields()
	brandingDir := os.Getenv("BRANDING_DIR")
	if brandingDir == "" {
		brandingDir = "./uploads"
	}

	r.With(requireAdmin).Get("/admin/panel", AdminPanelPage(deps, fields))
	r.With(requireAdmin).Post("/admin/panel/save-field", AdminPanelSaveField(deps, fields))
	r.With(requireAdmin).Get("/admin/panel/progress", AdminPanelProgress(deps, fields))
	r.With(requireAdmin).Post("/admin/panel/upload-file", AdminPanelUpload(deps, fields))
	r.With(requireAdmin).Post("/admin/panel/delete-file", AdminPanelDeleteFile(deps, fields))
}

// fieldByKey retourne le Field correspondant à la clé.
func fieldByKey(key string) (Field, bool) {
	for _, f := range V0Fields() {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// fieldProgress calcule la progression pour un ensemble de champs.
func fieldProgress(db *sql.DB, fields []Field) branding.ProgressInfo {
	defs := make([]branding.FieldDef, len(fields))
	for i, f := range fields {
		defs[i] = branding.FieldDef{Key: f.Key, Required: f.Required}
	}
	return branding.GetProgress(db, defs)
}

// toPanelFields convertit les Fields registry en PanelFields horui/admin.
func toPanelFields(fields []Field) []adminui.PanelField {
	out := make([]adminui.PanelField, len(fields))
	for i, f := range fields {
		out[i] = adminui.PanelField{
			Key: f.Key, Section: f.Section, Order: f.Order,
			Label: f.Label, Hint: f.Hint, Kind: f.Kind,
			Placeholder: f.Placeholder, Required: f.Required,
			MaxBytes: f.MaxBytes, MimeAllow: f.MimeAllow,
		}
	}
	return out
}

// toPanelBySec groupe les panelFields par section, triés par Order.
func toPanelBySec(fields []Field) map[string][]adminui.PanelField {
	bySection := FieldsBySection(fields)
	out := make(map[string][]adminui.PanelField, len(bySection))
	for sec, sFields := range bySection {
		pf := make([]adminui.PanelField, len(sFields))
		for i, f := range sFields {
			pf[i] = adminui.PanelField{
				Key: f.Key, Section: f.Section, Order: f.Order,
				Label: f.Label, Hint: f.Hint, Kind: f.Kind,
				Placeholder: f.Placeholder, Required: f.Required,
				MaxBytes: f.MaxBytes, MimeAllow: f.MimeAllow,
			}
		}
		out[sec] = pf
	}
	return out
}

// htmlEscape échappe les caractères HTML.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// AdminPanelPage — GET /admin/panel.
func AdminPanelPage(deps app.AppDeps, fields []Field) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values := make(map[string]string, len(fields))
		for _, f := range fields {
			values[f.Key] = branding.Get(deps.DB, f.Key)
		}
		progress := fieldProgress(deps.DB, fields)
		panelBySection := toPanelBySec(fields)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := adminui.AdminPanelPage(panelBySection, values, progress).Render(r.Context(), w); err != nil {
			deps.Logger.Error("admin panel render", "err", err)
		}
	}
}

// AdminPanelSaveField — POST /admin/panel/save-field.
func AdminPanelSaveField(deps app.AppDeps, fields []Field) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		key := strings.TrimSpace(r.FormValue("key"))
		value := r.FormValue("value")

		if key == "" {
			http.Error(w, "key manquant", http.StatusBadRequest)
			return
		}
		field, ok := fieldByKey(key)
		if !ok {
			http.Error(w, "champ inconnu", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)

		if err := ValidateField(field, value); err != nil {
			deps.Logger.Warn("admin_panel_field_validation_failed",
				"req_id", reqID,
				"key", key,
				"err", err.Error(),
			)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<span class="field-badge field-badge--error">` + htmlEscape(err.Error()) + `</span>`)) //nolint:errcheck
			return
		}

		userID := ""
		if u := middleware.UserFromContext(ctx); u != nil {
			userID = u.ID
		}

		if err := branding.Set(deps.DB, key, value, field.Kind, userID); err != nil {
			deps.Logger.Error("admin_panel_field_save_failed",
				"req_id", reqID,
				"user_id", userID,
				"key", key,
				"err", err.Error(),
			)
			http.Error(w, "erreur sauvegarde", http.StatusInternalServerError)
			return
		}

		deps.Logger.Info("admin_panel_field_saved",
			"req_id", reqID,
			"user_id", userID,
			"key", key,
		)

		pf := adminui.PanelField{
			Key: field.Key, Section: field.Section, Order: field.Order,
			Label: field.Label, Hint: field.Hint, Kind: field.Kind,
			Placeholder: field.Placeholder, Required: field.Required,
			MaxBytes: field.MaxBytes, MimeAllow: field.MimeAllow,
		}
		filled := value != ""
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := adminui.AdminFieldBadge(pf, value, filled).Render(r.Context(), w); err != nil {
			deps.Logger.Error("admin panel badge render", "err", err)
		}
	}
}

// AdminPanelProgress — GET /admin/panel/progress (JSON).
func AdminPanelProgress(deps app.AppDeps, fields []Field) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		progress := fieldProgress(deps.DB, fields)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{ //nolint:errcheck
			"required_total":  progress.RequiredTotal,
			"required_filled": progress.RequiredFilled,
			"all_total":       progress.AllTotal,
			"all_filled":      progress.AllFilled,
		})
	}
}
