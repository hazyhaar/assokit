package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/newsletter"
)

// initNewsletter enregistre les actions de diffusion (newsletter).
// Toutes passent par le registry → exposées en HTTP ET en MCP (LLM-parité) :
// le LLM de l'usager peut composer, lister et diffuser comme un admin humain.
func initNewsletter(reg *actions.Registry) {
	const perm = "newsletter.manage"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "newsletter.create",
		Title:        "Composer une diffusion",
		Description:  "Crée une diffusion (markdown) en brouillon, depuis l'admin courant.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["subject","body"],
			"properties":{
				"subject":{"type":"string","minLength":1},
				"body":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Subject string `json:"subject"`
				Body    string `json:"body"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &newsletter.Store{DB: deps.DB}
			id, err := store.Create(ctx, newsletter.Newsletter{
				Subject:   p.Subject,
				BodyMD:    p.Body,
				CreatedBy: u.ID,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Diffusion créée.", Data: map[string]any{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "newsletter.list",
		Title:        "Lister les diffusions",
		Description:  "Retourne les diffusions, les plus récentes d'abord, avec leur état (brouillon ou envoyée).",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			store := &newsletter.Store{DB: deps.DB}
			items, err := store.List(ctx)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			out := make([]map[string]any, 0, len(items))
			for _, n := range items {
				var sentAt any
				if n.SentAt.Valid {
					sentAt = n.SentAt.Time
				}
				out = append(out, map[string]any{
					"id":               n.ID,
					"subject":          n.Subject,
					"sent_at":          sentAt,
					"recipients_count": n.RecipientsCount,
					"created_at":       n.CreatedAt,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "newsletter.send",
		Title:        "Envoyer une diffusion",
		Description:  "Envoie la diffusion via l'outbox mail (best-effort), puis la marque comme envoyée. audience: 'all' (tous les comptes actifs, défaut) ou 'members' (adhérents avec adhésion active).",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"audience":{"type":"string","enum":["all","members"]}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			if deps.Mailer == nil {
				return actions.Result{Status: "error", Message: "mailer non configuré"}, nil
			}
			var p struct {
				ID       string `json:"id"`
				Audience string `json:"audience"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &newsletter.Store{DB: deps.DB}
			n, err := store.Get(ctx, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			// Audience : 'members' cible les adhésions actives ; sinon tous les
			// comptes actifs (défaut — cotisations = module optionnel, une
			// diffusion par défaut atteint toute la communauté).
			var emails []string
			if p.Audience == "members" {
				emails, err = store.ActiveMembershipEmails(ctx)
			} else {
				emails, err = store.ActiveMemberEmails(ctx)
			}
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			sent := 0
			for _, email := range emails {
				// Best-effort : l'outbox gère la livraison, les échecs unitaires
				// d'enqueue ne bloquent pas la diffusion.
				if err := deps.Mailer.Enqueue(ctx, email, n.Subject, n.BodyMD, n.BodyHTML); err != nil {
					deps.Logger.Error("newsletter_enqueue", "to", email, "err", err.Error())
					continue
				}
				sent++
			}
			if err := store.MarkSent(ctx, n.ID, sent); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Diffusion envoyée.", Data: map[string]any{"recipients_count": sent}}, nil
		},
	})
}
