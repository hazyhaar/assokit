package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initSignup(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.list",
		Title:        "Lister les inscriptions",
		Description:  "Retourne la liste des inscriptions en attente avec filtres optionnels.",
		RequiredPerm: "signup.list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["pending","active","rejected",""]},
				"limit":{"type":"integer","minimum":1,"maximum":100}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Status string `json:"status"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if p.Limit == 0 {
				p.Limit = 20
			}
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT id, email, status, created_at FROM users
				 WHERE (? = '' OR status = ?) ORDER BY created_at DESC LIMIT ?`,
				p.Status, p.Status, p.Limit,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, email, status, createdAt string
				if err := rows.Scan(&id, &email, &status, &createdAt); err != nil {
					continue
				}
				result = append(result, map[string]any{"id": id, "email": email, "status": status, "created_at": createdAt})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.activate",
		Title:        "Activer une inscription",
		Description:  "Active le compte d'un utilisateur inscrit.",
		RequiredPerm: "signup.activate",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE users SET status='active' WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Inscription activée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.reject",
		Title:        "Rejeter une inscription",
		Description:  "Rejette une demande d'inscription avec une raison.",
		RequiredPerm: "signup.reject",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id","reason"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"reason":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID     string `json:"id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE users SET status='rejected', rejection_reason=? WHERE id=?`,
				p.Reason, p.ID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Inscription rejetée."}, nil
		},
	})
}
