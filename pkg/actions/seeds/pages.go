package seeds

import (
	"context"
	"encoding/json"

	tree "github.com/hazyhaar/assokit/internal/nodetree"

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
				`SELECT id, slug, title, created_at FROM nodes
				 WHERE type='page' AND deleted_at IS NULL ORDER BY slug`,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, slug, title, createdAt string
				if err := rows.Scan(&id, &slug, &title, &createdAt); err != nil {
					continue
				}
				result = append(result, map[string]any{"id": id, "slug": slug, "title": title, "created_at": createdAt})
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
				"body_md":{"type":"string","minLength":1},
				"title":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Slug   string `json:"slug"`
				BodyMD string `json:"body_md"`
				Title  string `json:"title"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			title := p.Title
			if title == "" {
				title = p.Slug
			}
			store := &tree.Store{DB: deps.DB}
			id, err := store.Create(ctx, tree.Node{
				Slug:   p.Slug,
				Type:   "page",
				Title:  title,
				BodyMD: p.BodyMD,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page créée.", Data: map[string]any{"id": id, "slug": p.Slug}}, nil
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
				"body_md":{"type":"string","minLength":1},
				"title":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Slug   string `json:"slug"`
				BodyMD string `json:"body_md"`
				Title  string `json:"title"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &tree.Store{DB: deps.DB}
			node, err := store.GetBySlug(ctx, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			node.BodyMD = p.BodyMD
			if p.Title != "" {
				node.Title = p.Title
			}
			if err := store.Update(ctx, *node); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page mise à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "pages.delete",
		Title:        "Supprimer une page",
		Description:  "Supprime une page statique.",
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
			_, err := deps.DB.ExecContext(ctx, `DELETE FROM nodes WHERE slug=? AND type='page'`, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Page supprimée."}, nil
		},
	})
}
