package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/setup"
)

// initSetup enregistre l'action de diagnostic de configuration (menu Setup).
// Exposée en HTTP + MCP (LLM-parité) : le LLM peut diagnostiquer la config de
// l'instance pour aider l'admin à compléter ce qui manque.
func initSetup(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "setup.status",
		Title:        "Diagnostic de configuration",
		Description:  "Retourne l'état de configuration de chaque fonction du kit (configuré/manquant/partiel) et ce qu'il reste à renseigner.",
		RequiredPerm: "setup.read",
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(_ context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			items := setup.Status(deps)
			out := make([]map[string]any, 0, len(items))
			for _, it := range items {
				out = append(out, map[string]any{
					"key":      it.Key,
					"label":    it.Label,
					"state":    string(it.State),
					"required": it.Required,
					"hint":     it.Hint,
					"edit_url": it.EditURL,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})
}
