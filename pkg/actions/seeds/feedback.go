package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initFeedback(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "feedback.create",
		Title:        "Envoyer un feedback",
		Description:  "Crée un feedback utilisateur pour une page donnée (exposé MCP).",
		RequiredPerm: "feedback.create",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["page_url","message"],
			"properties":{
				"page_url":{"type":"string","minLength":1},
				"message":{"type":"string","minLength":1,"maxLength":5000}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				PageURL string `json:"page_url"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO feedbacks(id, page_url, message, status, created_at)
				 VALUES(hex(randomblob(8)), ?, ?, 'pending', CURRENT_TIMESTAMP)`,
				p.PageURL, p.Message,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Feedback enregistré."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "feedback.list",
		Title:        "Lister les feedbacks",
		Description:  "Retourne la liste des feedbacks avec filtres optionnels (modérateur).",
		RequiredPerm: "feedback.list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["pending","processed","archived",""]},
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
				`SELECT id, page_url, message, status, created_at FROM feedbacks
				 WHERE (? = '' OR status = ?) ORDER BY created_at DESC LIMIT ?`,
				p.Status, p.Status, p.Limit,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()

			var results []map[string]any
			for rows.Next() {
				var id, pageURL, message, status, createdAt string
				if err := rows.Scan(&id, &pageURL, &message, &status, &createdAt); err != nil {
					continue
				}
				results = append(results, map[string]any{
					"id": id, "page_url": pageURL, "message": message,
					"status": status, "created_at": createdAt,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: results}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "feedback.triage",
		Title:        "Traiter un feedback",
		Description:  "Change le statut d'un feedback et ajoute une note de traitement.",
		RequiredPerm: "feedback.triage",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id","status"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"status":{"type":"string","enum":["processed","archived"]},
				"note":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Note   string `json:"note"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE feedbacks SET status=?, triage_note=? WHERE id=?`,
				p.Status, p.Note, p.ID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Feedback traité."}, nil
		},
	})
}
