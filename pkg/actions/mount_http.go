package actions

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/perms"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// MountHTTP monte GET+POST /admin/actions/{action_id} pour chaque action du registry.
func MountHTTP(router chi.Router, deps app.AppDeps, reg *Registry) {
	for _, a := range reg.All() {
		action := a
		router.With(perms.Required(action.RequiredPerm)).
			Get("/admin/actions/"+action.ID, genericFormHandler(action))
		router.With(perms.Required(action.RequiredPerm)).
			Post("/admin/actions/"+action.ID, actionRunHandler(action, deps))
	}
}

func genericFormHandler(action Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = actionFormView(action, schemaFields(action.ParamsSchema)).Render(r.Context(), w)
	}
}

// schemaFields dérive les champs de formulaire du ParamsSchema, triés par nom
// (ordre stable du rendu).
func schemaFields(schema *jsonschema.Schema) []formField {
	if schema == nil {
		return nil
	}
	fields := make([]formField, 0, len(schema.Properties))
	for name, prop := range schema.Properties {
		inputType := "text"
		if prop != nil && len(prop.Types) > 0 {
			switch prop.Types[0] {
			case "integer", "number":
				inputType = "number"
			case "boolean":
				inputType = "checkbox"
			}
		}
		fields = append(fields, formField{Name: name, InputType: inputType})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

func actionRunHandler(action Action, deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paramsJSON, err := parseParams(r)
		if err != nil {
			http.Error(w, "paramètres invalides: "+err.Error(), http.StatusBadRequest)
			return
		}

		if action.ParamsSchema != nil {
			var v any
			if err := json.Unmarshal(paramsJSON, &v); err != nil {
				http.Error(w, "JSON invalide: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := action.ParamsSchema.Validate(v); err != nil {
				http.Error(w, "validation: "+err.Error(), http.StatusBadRequest)
				return
			}
		}

		result, err := action.Run(r.Context(), deps, paramsJSON)
		if err != nil {
			result = Result{Status: "error", Message: err.Error()}
		}

		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = actionResultView(result.Status, result.Message).Render(r.Context(), w)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func parseParams(r *http.Request) (json.RawMessage, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		return json.RawMessage(body), nil
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		r.ParseForm()
	}

	m := make(map[string]any)
	for k, vs := range r.Form {
		if len(vs) == 1 {
			m[k] = vs[0]
		} else {
			m[k] = vs
		}
	}
	return json.Marshal(m)
}
