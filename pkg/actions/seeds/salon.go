package seeds

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// initSalon enregistre les actions de gestion des salons de groupe.
// conn est optionnel (nil si le Vault LiveKit n'est pas configuré) :
// il est utilisé pour le cycle de vie best-effort (EndRoom à l'archivage /
// désactivation de la visio). Toutes les actions passent par le registry →
// exposées en HTTP ET en MCP.
func initSalon(reg *actions.Registry, conn *livekit.Connector) {
	const perm = "salon.use"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.create",
		Title:        "Créer un salon",
		Description:  "Crée un salon de groupe. Le créateur devient automatiquement membre (rôle owner). Le mot de passe est optionnel.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["name"],
			"properties":{
				"name":          {"type":"string","minLength":1},
				"password":      {"type":"string"},
				"visio_enabled": {"type":"boolean"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Name         string `json:"name"`
				Password     string `json:"password"`
				VisioEnabled bool   `json:"visio_enabled"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			sl, err := store.Create(ctx, p.Name, u.ID, p.Password, p.VisioEnabled)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Salon créé.", Data: map[string]any{
				"id":   sl.ID,
				"slug": sl.Slug,
				"name": sl.Name,
			}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.list",
		Title:        "Lister mes salons",
		Description:  "Retourne les salons dont l'utilisateur courant est membre.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			store := &salon.Store{DB: deps.DB}
			salons, err := store.List(ctx, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			out := make([]map[string]any, 0, len(salons))
			for _, sl := range salons {
				out = append(out, map[string]any{
					"id":            sl.ID,
					"name":          sl.Name,
					"slug":          sl.Slug,
					"visio_enabled": sl.VisioEnabled,
					"archived":      sl.ArchivedAt.Valid,
				})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.join",
		Title:        "Rejoindre un salon",
		Description:  "Rejoint un salon par son slug. Fournir le mot de passe si le salon est protégé.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{
				"slug":     {"type":"string","minLength":1},
				"password": {"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug     string `json:"slug"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			if err := store.Join(ctx, p.Slug, u.ID, p.Password); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Salon rejoint."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.leave",
		Title:        "Quitter un salon",
		Description:  "Retire l'utilisateur courant de la liste des membres du salon.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{"slug":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			if err := store.Leave(ctx, p.Slug, u.ID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Salon quitté."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.set_password",
		Title:        "Modifier le mot de passe d'un salon",
		Description:  "Change ou supprime le mot de passe du salon (chaîne vide = suppression).",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{
				"slug":     {"type":"string","minLength":1},
				"password": {"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug     string `json:"slug"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			if err := store.SetPassword(ctx, p.Slug, p.Password); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Mot de passe mis à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.toggle_visio",
		Title:        "Activer/désactiver la visio d'un salon",
		Description:  "Active ou désactive l'option visio pour le salon donné.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","on"],
			"properties":{
				"slug": {"type":"string","minLength":1},
				"on":   {"type":"boolean"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
				On   bool   `json:"on"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			// Lire l'état courant AVANT de basculer, pour décider si EndRoom est nécessaire.
			sl, err := store.Get(ctx, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := store.ToggleVisio(ctx, p.Slug, p.On); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			// Cycle de vie best-effort : si la visio était active et qu'on la désactive,
			// supprimer la salle LiveKit. Une erreur ici ne bloque pas la mise à jour DB.
			if conn != nil && sl.VisioEnabled && !p.On {
				if err := conn.EndRoom(ctx, p.Slug); err != nil {
					slog.WarnContext(ctx, "salon.toggle_visio: EndRoom best-effort échoué", "room", p.Slug, "err", err)
				}
			}
			return actions.Result{Status: "ok", Message: "Option visio mise à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.post_message",
		Title:        "Poster un message dans un salon",
		Description:  "Publie un message (markdown) dans le salon. body_html est généré automatiquement.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","body"],
			"properties":{
				"slug": {"type":"string","minLength":1},
				"body": {"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
				Body string `json:"body"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			id, err := store.PostMessage(ctx, p.Slug, u.ID, p.Body)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{Status: "ok", Message: "Message posté.", Data: map[string]any{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "salon.archive",
		Title:        "Archiver un salon",
		Description:  "Archive le salon (archived_at = now). Les messages sont conservés en lecture seule.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{"slug":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			// Lire l'état courant AVANT d'archiver pour décider si EndRoom est nécessaire.
			sl, err := store.Get(ctx, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := store.Archive(ctx, p.Slug); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			// Cycle de vie best-effort : si la visio était active, supprimer la salle
			// LiveKit. Une erreur ici ne bloque pas l'archivage.
			if conn != nil && sl.VisioEnabled {
				if err := conn.EndRoom(ctx, p.Slug); err != nil {
					slog.WarnContext(ctx, "salon.archive: EndRoom best-effort échoué", "room", p.Slug, "err", err)
				}
			}
			return actions.Result{Status: "ok", Message: "Salon archivé."}, nil
		},
	})
}
