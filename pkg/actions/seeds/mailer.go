package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initMailer(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "mailer.outbox.list",
		Title:        "Lister l'outbox mailer",
		Description:  "Retourne les emails en attente ou en erreur dans l'outbox.",
		RequiredPerm: "mailer.outbox.list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["pending","sent","error","cancelled",""]},
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
				`SELECT id, to_addr, subject, status, created_at FROM mailer_outbox
				 WHERE (? = '' OR status = ?) ORDER BY created_at DESC LIMIT ?`,
				p.Status, p.Status, p.Limit,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, toAddr, subject, status, createdAt string
				if err := rows.Scan(&id, &toAddr, &subject, &status, &createdAt); err != nil {
					continue
				}
				result = append(result, map[string]any{
					"id": id, "to": toAddr, "subject": subject,
					"status": status, "created_at": createdAt,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "mailer.outbox.retry",
		Title:        "Relancer un email en erreur",
		Description:  "Replanifie l'envoi d'un email en erreur dans l'outbox.",
		RequiredPerm: "mailer.outbox.retry",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE mailer_outbox SET status='pending', retry_at=CURRENT_TIMESTAMP WHERE id=?`,
				p.ID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Email replanifié."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "mailer.outbox.cancel",
		Title:        "Annuler un email",
		Description:  "Annule l'envoi d'un email en attente dans l'outbox.",
		RequiredPerm: "mailer.outbox.cancel",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE mailer_outbox SET status='cancelled' WHERE id=? AND status='pending'`,
				p.ID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Email annulé."}, nil
		},
	})
}
