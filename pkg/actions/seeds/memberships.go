package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/membership"
)

// initMemberships enregistre les actions de cotisations/adhésions. Toutes passent
// par le registry → exposées en HTTP ET en MCP (LLM-parité). Les écritures
// (record, set_status) sont réservées à la permission de gestion ; un membre ne
// peut voir que SES adhésions (memberships.view_self).
func initMemberships(reg *actions.Registry) {
	const permManage = "memberships.manage"
	const permViewSelf = "memberships.view_self"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "memberships.record",
		Title:        "Enregistrer une adhésion",
		Description:  "Enregistre une adhésion/cotisation pour un membre (période, montant en centimes, statut, note).",
		RequiredPerm: permManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["user_id","period_start","period_end"],
			"properties":{
				"user_id":{"type":"string","minLength":1},
				"period_start":{"type":"string","minLength":1},
				"period_end":{"type":"string","minLength":1},
				"amount_cents":{"type":"integer","minimum":0},
				"status":{"type":"string","enum":["pending","active","expired","cancelled"]},
				"note":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UserID      string `json:"user_id"`
				PeriodStart string `json:"period_start"`
				PeriodEnd   string `json:"period_end"`
				AmountCents int64  `json:"amount_cents"`
				Status      string `json:"status"`
				Note        string `json:"note"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &membership.Store{DB: deps.DB}
			id, err := store.Create(ctx, membership.Membership{
				UserID:      p.UserID,
				PeriodStart: p.PeriodStart,
				PeriodEnd:   p.PeriodEnd,
				AmountCents: p.AmountCents,
				Status:      p.Status,
				Note:        p.Note,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Adhésion enregistrée.", Data: map[string]any{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "memberships.list",
		Title:        "Lister les adhésions",
		Description:  "Retourne toutes les adhésions, optionnellement filtrées par statut.",
		RequiredPerm: permManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"status":{"type":"string","enum":["pending","active","expired","cancelled"]}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Status string `json:"status"`
			}
			if len(params) > 0 {
				if err := json.Unmarshal(params, &p); err != nil {
					return actions.Result{Status: "error", Message: err.Error()}, nil
				}
			}
			store := &membership.Store{DB: deps.DB}
			list, err := store.ListAll(ctx, p.Status)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "ok", Data: encodeMemberships(list)}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "memberships.set_status",
		Title:        "Changer le statut d'une adhésion",
		Description:  "Met à jour le statut d'une adhésion (pending, active, expired, cancelled).",
		RequiredPerm: permManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id","status"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"status":{"type":"string","enum":["pending","active","expired","cancelled"]}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &membership.Store{DB: deps.DB}
			if err := store.SetStatus(ctx, p.ID, p.Status); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Statut mis à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "memberships.mine",
		Title:        "Mes adhésions",
		Description:  "Retourne les adhésions de l'utilisateur courant.",
		RequiredPerm: permViewSelf,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			store := &membership.Store{DB: deps.DB}
			list, err := store.ListForUser(ctx, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "ok", Data: encodeMemberships(list)}, nil
		},
	})
}

func encodeMemberships(list []membership.Membership) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]any{
			"id":           m.ID,
			"user_id":      m.UserID,
			"period_start": m.PeriodStart,
			"period_end":   m.PeriodEnd,
			"amount_cents": m.AmountCents,
			"status":       m.Status,
			"note":         m.Note,
			"created_at":   m.CreatedAt,
		})
	}
	return out
}
