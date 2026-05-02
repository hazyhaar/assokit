package actions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
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
		var sb strings.Builder
		fmt.Fprintf(&sb, `<div class="action-form"><h2>%s</h2><p>%s</p>`, action.Title, action.Description)
		fmt.Fprintf(&sb, `<form method="POST" action="/admin/actions/%s" hx-post="/admin/actions/%s" hx-target="#action-result" hx-swap="innerHTML">`,
			action.ID, action.ID)

		if action.ParamsSchema != nil {
			for propName, propSchema := range action.ParamsSchema.Properties {
				inputType := "text"
				if propSchema != nil && len(propSchema.Types) > 0 {
					switch propSchema.Types[0] {
					case "integer", "number":
						inputType = "number"
					case "boolean":
						inputType = "checkbox"
					}
				}
				fmt.Fprintf(&sb, `<div><label>%s <input type="%s" name="%s"/></label></div>`, propName, inputType, propName)
			}
		}

		sb.WriteString(`<button type="submit">Exécuter</button></form><div id="action-result"></div></div>`)
		io.WriteString(w, sb.String())
	}
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
			cls := "ok"
			if result.Status != "ok" {
				cls = "error"
			}
			fmt.Fprintf(w, `<div class="action-result %s"><strong>%s</strong> — %s</div>`, cls, result.Status, result.Message)
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
