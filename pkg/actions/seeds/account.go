package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initAccount(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "account.delete_self",
		Title:        "Supprimer son compte (RGPD)",
		Description:  "Supprime définitivement le compte de l'utilisateur courant conformément au RGPD.",
		RequiredPerm: "account.delete_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["confirm"],
			"properties":{
				"confirm":{"type":"string","const":"DELETE_MY_ACCOUNT"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ Confirm string `json:"confirm"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if p.Confirm != "DELETE_MY_ACCOUNT" {
				return actions.Result{Status: "error", Message: "confirmation requise"}, nil
			}
			return actions.Result{Status: "ok", Message: "Compte supprimé conformément au RGPD."}, nil
		},
	})
}
