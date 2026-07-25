package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/messaging"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// initMessages enregistre les actions de messagerie privée membre↔membre.
// Toutes passent par le registry → exposées en HTTP ET en MCP (LLM-parité) :
// le LLM de l'usager peut lire et écrire des DM comme un membre humain.
func initMessages(reg *actions.Registry) {
	const perm = "messages.use"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "messages.send",
		Title:        "Envoyer un message privé",
		Description:  "Envoie un message privé (markdown) à un autre membre, depuis l'utilisateur courant.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["recipient_id","body"],
			"properties":{
				"recipient_id":{"type":"string","minLength":1},
				"body":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				RecipientID string `json:"recipient_id"`
				Body        string `json:"body"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &messaging.Store{DB: deps.DB}
			id, err := store.Send(ctx, u.ID, p.RecipientID, p.Body)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Message envoyé.", Data: map[string]any{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "messages.inbox",
		Title:        "Lister mes conversations",
		Description:  "Retourne les conversations de l'utilisateur courant : dernier message et nombre de non-lus par correspondant.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			store := &messaging.Store{DB: deps.DB}
			convs, err := store.Inbox(ctx, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			out := make([]map[string]any, 0, len(convs))
			for _, c := range convs {
				out = append(out, map[string]any{
					"with_user_id": c.WithUserID,
					"with_name":    c.WithName,
					"with_email":   c.WithEmail,
					"last_body":    c.LastBody,
					"last_at":      c.LastAt,
					"unread":       c.Unread,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "messages.thread",
		Title:        "Lire un fil de conversation",
		Description:  "Retourne les messages échangés avec un membre donné (du plus ancien au plus récent) et marque ceux reçus comme lus.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["with_user_id"],
			"properties":{
				"with_user_id":{"type":"string","minLength":1},
				"limit":{"type":"integer","minimum":1,"maximum":500}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				WithUserID string `json:"with_user_id"`
				Limit      int    `json:"limit"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &messaging.Store{DB: deps.DB}
			msgs, err := store.Thread(ctx, u.ID, p.WithUserID, p.Limit)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			_, _ = store.MarkRead(ctx, u.ID, p.WithUserID)
			out := make([]map[string]any, 0, len(msgs))
			for _, m := range msgs {
				out = append(out, map[string]any{
					"id":        m.ID,
					"from_me":   m.SenderID == u.ID,
					"body":      m.BodyMD,
					"body_html": m.BodyHTML,
					"sent_at":   m.CreatedAt,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "messages.mark_read",
		Title:        "Marquer un fil comme lu",
		Description:  "Marque comme lus les messages reçus en provenance d'un membre donné.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["with_user_id"],
			"properties":{"with_user_id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				WithUserID string `json:"with_user_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &messaging.Store{DB: deps.DB}
			n, err := store.MarkRead(ctx, u.ID, p.WithUserID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "ok", Data: map[string]any{"marked": n}}, nil
		},
	})
}
