package actions

import (
	"encoding/json"
	"net/http"
	"strings"
)

const destructiveConfirmField = "_destructive_confirm"

// destructiveConfirmExpected détermine la valeur attendue de confirmation :
// l'identifiant de la cible (premier champ id connu) ou « SUPPRIMER ».
func destructiveConfirmExpected(params map[string]any) string {
	for _, k := range []string{"acte_id", "layer_id", "uid", "id"} {
		if v, ok := params[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return "SUPPRIMER"
}

// parseParams est conservé pour compatibilité interne des tests.
func parseParams(r *http.Request) (json.RawMessage, error) {
	raw, _, err := parseParamsRaw(r)
	return raw, err
}

func parseFormMap(r *http.Request) (map[string]any, error) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		r.ParseForm()
	}
	m := make(map[string]any, len(r.Form))
	for k, vs := range r.Form {
		if len(vs) == 1 {
			m[k] = vs[0]
		} else {
			m[k] = vs
		}
	}
	return m, nil
}

func extractDestructiveConfirm(m map[string]any) string {
	if v, ok := m[destructiveConfirmField]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func stripDestructiveConfirm(m map[string]any) {
	delete(m, destructiveConfirmField)
}

func mapToRawJSON(m map[string]any) (json.RawMessage, error) {
	return json.Marshal(m)
}
