package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
)

func initUsers(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.list",
		Title:        "Lister les utilisateurs",
		Description:  "Retourne la liste paginée des utilisateurs avec filtres optionnels.",
		RequiredPerm: "users.list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"search":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"offset":{"type":"integer","minimum":0}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
				Offset int    `json:"offset"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if p.Limit == 0 {
				p.Limit = 20
			}
			search := "%" + p.Search + "%"
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT id, email, created_at FROM users
				 WHERE (? = '%%' OR email LIKE ?) LIMIT ? OFFSET ?`,
				search, search, p.Limit, p.Offset,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			defer rows.Close()
			var result []map[string]any
			for rows.Next() {
				var id, email, createdAt string
				if err := rows.Scan(&id, &email, &createdAt); err != nil {
					continue
				}
				result = append(result, map[string]any{"id": id, "email": email, "created_at": createdAt})
			}
			return actions.Result{Status: "ok", Message: "ok", Data: result}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.role_assign",
		Title:        "Assigner un grade à un utilisateur",
		Description:  "Assigne un grade RBAC à un utilisateur.",
		RequiredPerm: "users.role_assign",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["uid","grade_id"],
			"properties":{
				"uid":{"type":"string","minLength":1},
				"grade_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UID     string `json:"uid"`
				GradeID string `json:"grade_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES(?,?)`,
				p.UID, p.GradeID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Grade assigné."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.role_remove",
		Title:        "Retirer un grade d'un utilisateur",
		Description:  "Retire un grade RBAC d'un utilisateur.",
		RequiredPerm: "users.role_remove",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["uid","grade_id"],
			"properties":{
				"uid":{"type":"string","minLength":1},
				"grade_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UID     string `json:"uid"`
				GradeID string `json:"grade_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`DELETE FROM user_grades WHERE user_id=? AND grade_id=?`,
				p.UID, p.GradeID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Grade retiré."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.deactivate",
		Title:        "Désactiver un compte utilisateur",
		Description:  "Désactive le compte d'un utilisateur avec une raison.",
		RequiredPerm: "users.deactivate",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["uid","reason"],
			"properties":{
				"uid":{"type":"string","minLength":1},
				"reason":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UID    string `json:"uid"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE users SET active=0, deactivation_reason=? WHERE id=?`,
				p.Reason, p.UID,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Compte désactivé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.reactivate",
		Title:        "Réactiver un compte utilisateur",
		Description:  "Réactive un compte préalablement désactivé.",
		RequiredPerm: "users.reactivate",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["uid"],
			"properties":{"uid":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UID string `json:"uid"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `UPDATE users SET active=1 WHERE id=?`, p.UID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Compte réactivé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "users.delete",
		Title:        "Supprimer un compte (RGPD admin override)",
		Description:  "Supprime définitivement un compte et ses données (RGPD, admin seulement).",
		RequiredPerm: "users.delete",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["uid"],
			"properties":{"uid":{"type":"string","minLength":1}}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				UID string `json:"uid"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx, `DELETE FROM users WHERE id=?`, p.UID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Compte supprimé définitivement."}, nil
		},
	})
}
