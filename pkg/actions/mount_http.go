package actions

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
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
		rawJSON, confirm, err := parseParamsRaw(r)
		if err != nil {
			writeActionRunError(w, r, "paramètres invalides: "+err.Error())
			return
		}

		if action.Destructive {
			if err := validateDestructiveConfirm(rawJSON, confirm); err != nil {
				writeActionRunError(w, r, err.Error())
				return
			}
		}

		paramsJSON, err := stripConfirmFromJSON(rawJSON)
		if err != nil {
			writeActionRunError(w, r, "paramètres invalides: "+err.Error())
			return
		}

		if action.ParamsSchema != nil {
			var v any
			if err := json.Unmarshal(paramsJSON, &v); err != nil {
				writeActionRunError(w, r, "JSON invalide: "+err.Error())
				return
			}
			if err := action.ParamsSchema.Validate(v); err != nil {
				writeActionRunError(w, r, "validation: "+err.Error())
				return
			}
		}

		result, err := action.Run(r.Context(), deps, paramsJSON)
		if err != nil {
			result = Result{Status: "error", Message: err.Error()}
		}

		if action.Destructive && result.Status == "ok" && err == nil {
			logDestructiveAction(r, deps, action.ID, paramsJSON)
		}

		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = actionResultView(result.Status, result.Message).Render(r.Context(), w)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result) //nolint:errcheck
	}
}

// writeActionRunError rend l'erreur dans #action-result pour les requêtes HTMX
// (sinon la page reste muette car htmx n'échange pas les réponses 4xx par défaut).
func writeActionRunError(w http.ResponseWriter, r *http.Request, message string) {
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = actionResultView("error", message).Render(r.Context(), w)
		return
	}
	http.Error(w, message, http.StatusBadRequest)
}

func parseParamsRaw(r *http.Request) (json.RawMessage, string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, "", err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err == nil {
			confirm := extractDestructiveConfirm(raw)
			out, merr := mapToRawJSON(raw)
			return out, confirm, merr
		}
		return json.RawMessage(body), "", nil
	}

	m, err := parseFormMap(r)
	if err != nil {
		return nil, "", err
	}
	confirm := extractDestructiveConfirm(m)
	out, err := mapToRawJSON(m)
	return out, confirm, err
}

func stripConfirmFromJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil
	}
	stripDestructiveConfirm(m)
	return mapToRawJSON(m)
}

func validateDestructiveConfirm(paramsJSON json.RawMessage, confirm string) error {
	var params map[string]any
	if len(paramsJSON) > 0 {
		_ = json.Unmarshal(paramsJSON, &params)
	}
	expected := destructiveConfirmExpected(params)

	confirm = strings.TrimSpace(confirm)
	if confirm == "" {
		return errDestructiveConfirmMissing()
	}
	if confirm != expected {
		return errDestructiveConfirmMismatch(expected)
	}
	return nil
}

type destructiveConfirmError string

func (e destructiveConfirmError) Error() string { return string(e) }

func errDestructiveConfirmMissing() error {
	return destructiveConfirmError("confirmation typée requise pour cette action irréversible")
}

func errDestructiveConfirmMismatch(expected string) error {
	return destructiveConfirmError("confirmation incorrecte : attendu « " + expected + " » ou identifiant exact de la cible")
}

func logDestructiveAction(r *http.Request, deps app.AppDeps, actionID string, params json.RawMessage) {
	actorID := perms.UserID(r.Context())
	if u := middleware.UserFromContext(r.Context()); u != nil && u.ID != "" {
		actorID = u.ID
	}
	if actorID == "" {
		actorID = "system"
	}
	subjectID := actionID
	var m map[string]json.RawMessage
	if json.Unmarshal(params, &m) == nil {
		for _, k := range []string{"acte_id", "layer_id", "uid", "id"} {
			if raw, ok := m[k]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil && s != "" {
					subjectID = s
					break
				}
			}
		}
	}
	gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		SubjectKind: "action_destructive",
		SubjectID:   subjectID,
		ActorID:     actorID,
		Action:      actionID,
	})
}
