package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initForum(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.create",
		Title:        "Publier un message forum",
		Description:  "Crée un nouveau post dans un thread forum identifié par son slug.",
		RequiredPerm: "forum.post.create",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"required":["thread_slug","message"],
			"properties":{
				"thread_slug":{"type":"string","minLength":1},
				"message":{"type":"string","minLength":1,"maxLength":10000}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ThreadSlug string `json:"thread_slug"`
				Message    string `json:"message"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO nodes(id, slug, parent_id, kind, body, created_at)
				 SELECT hex(randomblob(8)), hex(randomblob(8)), id, 'reply', ?, CURRENT_TIMESTAMP
				 FROM nodes WHERE slug=? AND kind='thread'`,
				p.Message, p.ThreadSlug,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Message publié."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.reply.create",
		Title:        "Répondre à un post forum",
		Description:  "Crée une réponse à un post forum existant.",
		RequiredPerm: "forum.post.create",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"required":["parent_id","message"],
			"properties":{
				"parent_id":{"type":"string","minLength":1},
				"message":{"type":"string","minLength":1,"maxLength":10000}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ParentID string `json:"parent_id"`
				Message  string `json:"message"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT INTO nodes(id, slug, parent_id, kind, body, created_at)
				 VALUES(hex(randomblob(8)), hex(randomblob(8)), ?, 'reply', ?, CURRENT_TIMESTAMP)`,
				p.ParentID, p.Message,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Réponse publiée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.edit_self",
		Title:        "Modifier son propre post forum",
		Description:  "Modifie le contenu d'un post appartenant à l'utilisateur courant.",
		RequiredPerm: "forum.post.edit_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"required":["id","message"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"message":{"type":"string","minLength":1,"maxLength":10000}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET body=? WHERE id=?`, p.Message, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Post modifié."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.delete_self",
		Title:        "Supprimer son propre post forum",
		Description:  "Supprime un post appartenant à l'utilisateur courant.",
		RequiredPerm: "forum.post.delete_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Post supprimé."}, nil
		},
	})

	// --- Modérateur ---

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.delete",
		Title:        "Supprimer un post (modérateur)",
		Description:  "Supprime n'importe quel post forum avec une raison de modération.",
		RequiredPerm: "forum.post.delete",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"required":["id","reason"],
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
			_, err := deps.DB.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Post supprimé par modération. Raison : " + p.Reason}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.pin",
		Title:        "Épingler un post forum",
		Description:  "Épingle un post en tête de son thread.",
		RequiredPerm: "forum.post.pin",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET pinned=1 WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Post épinglé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.post.unpin",
		Title:        "Désépingler un post forum",
		Description:  "Retire l'épingle d'un post forum.",
		RequiredPerm: "forum.post.pin",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET pinned=0 WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Post désépinglé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.thread.lock",
		Title:        "Verrouiller un thread forum",
		Description:  "Empêche toute nouvelle réponse dans un thread.",
		RequiredPerm: "forum.thread.lock",
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
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET locked=1 WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Thread verrouillé. Raison : " + p.Reason}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.thread.unlock",
		Title:        "Déverrouiller un thread forum",
		Description:  "Réouvre un thread précédemment verrouillé.",
		RequiredPerm: "forum.thread.lock",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{"id":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct{ ID string `json:"id"` }
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET locked=0 WHERE id=?`, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Thread déverrouillé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.thread.move",
		Title:        "Déplacer un thread forum",
		Description:  "Déplace un thread vers un autre parent (catégorie).",
		RequiredPerm: "forum.thread.move",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id","dest_parent"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"dest_parent":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID         string `json:"id"`
				DestParent string `json:"dest_parent"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE nodes SET parent_id=? WHERE id=?`, p.DestParent, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Thread déplacé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.user.warn",
		Title:        "Avertir un utilisateur forum",
		Description:  "Envoie un avertissement à un utilisateur avec une raison.",
		RequiredPerm: "forum.user.warn",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["user_id","reason"],
			"properties":{
				"user_id":{"type":"string","minLength":1},
				"reason":{"type":"string","minLength":1}
			}
		}`),
		Run: func(_ context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UserID string `json:"user_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Avertissement envoyé à " + p.UserID + ". Raison : " + p.Reason}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "forum.user.timeout",
		Title:        "Bannir temporairement un utilisateur forum",
		Description:  "Timeout temporaire d'un utilisateur pour un nombre d'heures donné.",
		RequiredPerm: "forum.user.timeout",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["user_id","hours","reason"],
			"properties":{
				"user_id":{"type":"string","minLength":1},
				"hours":{"type":"integer","minimum":1,"maximum":720},
				"reason":{"type":"string","minLength":1}
			}
		}`),
		Run: func(_ context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UserID string `json:"user_id"`
				Hours  int    `json:"hours"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Utilisateur timeout 24h. Raison : " + p.Reason}, nil
		},
	})
}
