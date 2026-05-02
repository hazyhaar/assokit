package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initPages(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "pages.list",
		Title:        "Lister les pages",
		Description:  "Retourne la liste des pages statiques du site.",
		RequiredPerm: "pages.list",
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT id, slug, kind, created_at FROM nodes WHERE kind='page' ORDER BY slug`,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, slug, kind, createdAt string
				if err := rows.Scan(&id, &slug, &kind, &createdAt); err != nil {
					continue
				}
				result = append(result, map[string]any{"id": id, "slug": slug, "kind": kind, "created_at": createdAt})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "pages.create",
		Title:        "Créer une page",
		Description:  "Crée une nouvelle page statique avec un slug et un corps Markdown.",
		RequiredPerm: "pages.create",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","body_md"],
			"properties":{
				"slug":{"type":"string","minLength":1,"pattern":"^[a-z0-9-]+$"},
				"body_md":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Slug   string `json:"slug"`
				BodyMD string `json:"body_md"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO nodes(id, slug, kind, body, created_at)
				 VALUES(hex(randomblob(8)), ?, 'page', ?, CURRENT_TIMESTAMP)`,
				p.Slug, p.BodyMD,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page créée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "pages.update",
		Title:        "Mettre à jour une page",
		Description:  "Met à jour le contenu Markdown d'une page existante.",
		RequiredPerm: "pages.update",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","body_md"],
			"properties":{
				"slug":{"type":"string","minLength":1},
				"body_md":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Slug   string `json:"slug"`
				BodyMD string `json:"body_md"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE nodes SET body=? WHERE slug=? AND kind='page'`,
				p.BodyMD, p.Slug,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page mise à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "pages.delete",
		Title:        "Supprimer une page",
		Description:  "Supprime définitivement une page statique.",
		RequiredPerm: "pages.delete",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{"slug":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `DELETE FROM nodes WHERE slug=? AND kind='page'`, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page supprimée."}, nil
		},
	})
}
