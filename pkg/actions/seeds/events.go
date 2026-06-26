package seeds

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/events"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// initEvents enregistre les actions agenda/événements. Toutes passent par le
// registry → exposées en HTTP ET en MCP (LLM-parité). Liste/consultation sous
// events.read (publiques) ; création/suppression sous events.manage.
func initEvents(reg *actions.Registry) {
	const permRead = "events.read"
	const permManage = "events.manage"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "events.create",
		Title:        "Créer un événement",
		Description:  "Crée un événement (titre, description markdown, lieu, début/fin). Slug auto-généré depuis le titre si absent.",
		RequiredPerm: permManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["title","starts_at"],
			"properties":{
				"title":{"type":"string","minLength":1},
				"slug":{"type":"string"},
				"description":{"type":"string"},
				"location":{"type":"string"},
				"starts_at":{"type":"string","minLength":1},
				"ends_at":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			var p struct {
				Title       string `json:"title"`
				Slug        string `json:"slug"`
				Description string `json:"description"`
				Location    string `json:"location"`
				StartsAt    string `json:"starts_at"`
				EndsAt      string `json:"ends_at"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			e := events.Event{
				Slug:     p.Slug,
				Title:    p.Title,
				DescMD:   p.Description,
				Location: p.Location,
				StartsAt: p.StartsAt,
			}
			if p.EndsAt != "" {
				e.EndsAt = sql.NullString{String: p.EndsAt, Valid: true}
			}
			if u != nil {
				e.CreatedBy = sql.NullString{String: u.ID, Valid: true}
			}
			store := &events.Store{DB: deps.DB}
			id, err := store.Create(ctx, e)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Événement créé.", Data: map[string]any{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "events.list",
		Title:        "Lister les événements",
		Description:  "Retourne les événements de l'agenda (non supprimés), à venir d'abord.",
		RequiredPerm: permRead,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			store := &events.Store{DB: deps.DB}
			list, err := store.List(ctx)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, e := range list {
				out = append(out, eventToMap(e))
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "events.get",
		Title:        "Voir un événement",
		Description:  "Retourne un événement par son id ou son slug.",
		RequiredPerm: permRead,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id_or_slug"],
			"properties":{"id_or_slug":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				IDOrSlug string `json:"id_or_slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &events.Store{DB: deps.DB}
			e, err := store.Get(ctx, p.IDOrSlug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "ok", Data: eventToMap(e)}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "events.delete",
		Title:        "Supprimer un événement",
		Description:  "Marque un événement comme supprimé (soft-delete).",
		RequiredPerm: permManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &events.Store{DB: deps.DB}
			if err := store.SoftDelete(ctx, p.ID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Événement supprimé."}, nil
		},
	})
}

// eventToMap sérialise un événement pour les résultats d'action.
func eventToMap(e events.Event) map[string]any {
	m := map[string]any{
		"id":        e.ID,
		"slug":      e.Slug,
		"title":     e.Title,
		"location":  e.Location,
		"starts_at": e.StartsAt,
		"body_html": e.DescHTML,
	}
	if e.EndsAt.Valid {
		m["ends_at"] = e.EndsAt.String
	}
	return m
}
