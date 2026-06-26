package seeds

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initSignup(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.list",
		Title:        "Lister les inscriptions",
		Description:  "Retourne la liste des inscriptions en attente avec filtres optionnels.",
		RequiredPerm: "signup.list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["pending","active","rejected",""]},
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
			// Lecture depuis la table réelle `signups`. Il n'existe pas de colonne
			// `status` : le traitement est matérialisé par `processed_at`
			// (NULL = en attente). On range les non traités d'abord, puis du plus
			// récent au plus ancien. Le filtre optionnel `status` est mappé sur
			// l'état de traitement (`pending` = non traité, sinon traité).
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT id, email, display_name, profile, created_at, processed_at
				 FROM signups
				 WHERE (? = ''
				        OR (? = 'pending' AND processed_at IS NULL)
				        OR (? <> 'pending' AND processed_at IS NOT NULL))
				 ORDER BY (processed_at IS NOT NULL), created_at DESC
				 LIMIT ?`,
				p.Status, p.Status, p.Status, p.Limit,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, email, displayName, profile, createdAt string
				var processedAt sql.NullString
				if err := rows.Scan(&id, &email, &displayName, &profile, &createdAt, &processedAt); err != nil {
					continue
				}
				item := map[string]any{
					"id":           id,
					"email":        email,
					"display_name": displayName,
					"profile":      profile,
					"created_at":   createdAt,
					"processed_at": nil,
				}
				if processedAt.Valid {
					item["processed_at"] = processedAt.String
				}
				result = append(result, item)
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.activate",
		Title:        "Activer une inscription",
		Description:  "Active le compte d'un utilisateur inscrit.",
		RequiredPerm: "signup.activate",
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
			// Active le compte correspondant (users.is_active = 1 ; il n'existe pas
			// de colonne users.status). L'`id` reçu est l'identifiant du compte.
			if _, err := deps.DB.ExecContext(ctx,
				`UPDATE users SET is_active=1 WHERE id=?`, p.ID,
			); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			// Marque l'inscription correspondante comme traitée (best-effort :
			// signups.processed_at). Sans colonne de statut, processed_at
			// matérialise le traitement. Aucun effet si aucune ligne ne correspond.
			if _, err := deps.DB.ExecContext(ctx,
				`UPDATE signups SET processed_at=CURRENT_TIMESTAMP
				 WHERE id=? AND processed_at IS NULL`, p.ID,
			); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Inscription activée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "signup.reject",
		Title:        "Rejeter une inscription",
		Description:  "Rejette une demande d'inscription avec une raison.",
		RequiredPerm: "signup.reject",
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
			// Rejette le compte correspondant : désactivation (users.is_active = 0).
			// Il n'existe pas de colonne users.status ni rejection_reason ; la raison
			// reste informative (retour au demandeur), non persistée faute de colonne.
			if _, err := deps.DB.ExecContext(ctx,
				`UPDATE users SET is_active=0 WHERE id=?`, p.ID,
			); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			// Marque l'inscription correspondante comme traitée (rejet) via
			// signups.processed_at. Best-effort, sans effet si aucune ligne ne matche.
			if _, err := deps.DB.ExecContext(ctx,
				`UPDATE signups SET processed_at=CURRENT_TIMESTAMP
				 WHERE id=? AND processed_at IS NULL`, p.ID,
			); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Inscription rejetée."}, nil
		},
	})
}
