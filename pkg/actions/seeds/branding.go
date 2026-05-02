package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initBranding(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "branding.get",
		Title:        "Lire la configuration branding",
		Description:  "Retourne les paramètres de branding actuels (nom, couleurs, logo, footer).",
		RequiredPerm: "branding.read",
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT field, value FROM branding ORDER BY field`,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			result := make(map[string]string)
			for rows.Next() {
				var field, value string
				if err := rows.Scan(&field, &value); err != nil {
					continue
				}
				result[field] = value
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	validBrandingFields := map[string]bool{
		"site_name": true, "primary_color": true, "secondary_color": true,
		"logo_url": true, "footer_text": true, "footer_links": true,
	}

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "branding.set",
		Title:        "Modifier un paramètre branding",
		Description:  "Met à jour un champ de branding parmi les champs autorisés.",
		RequiredPerm: "branding.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["field","value"],
			"properties":{
				"field":{"type":"string","enum":["site_name","primary_color","secondary_color","logo_url","footer_text","footer_links"]},
				"value":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Field string `json:"field"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if !validBrandingFields[p.Field] {
				return actions.Result{Status: "error", Message: "champ branding inconnu: " + p.Field}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO branding(field, value) VALUES(?,?)
				 ON CONFLICT(field) DO UPDATE SET value=excluded.value`,
				p.Field, p.Value,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Branding mis à jour."}, nil
		},
	})
}
