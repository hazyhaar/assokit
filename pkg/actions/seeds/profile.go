package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func initProfile(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "profile.edit_self",
		Title:        "Modifier son profil",
		Description:  "Met à jour les informations de profil de l'utilisateur courant.",
		RequiredPerm: "profile.edit_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"display_name":{"type":"string","maxLength":100},
				"bio":{"type":"string","maxLength":1000},
				"website":{"type":"string","format":"uri"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				DisplayName string `json:"display_name"`
				Bio         string `json:"bio"`
				Website     string `json:"website"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			_, err := deps.DB.ExecContext(ctx,
				`UPDATE users SET display_name=COALESCE(NULLIF(?,'''), display_name),
				 bio=COALESCE(NULLIF(?,''), bio), website=COALESCE(NULLIF(?,''), website)
				 WHERE id=(SELECT id FROM users LIMIT 1)`,
				p.DisplayName, p.Bio, p.Website,
			)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Profil mis à jour."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "profile.avatar_upload",
		Title:        "Uploader un avatar",
		Description:  "Remplace l'avatar de l'utilisateur courant par une URL d'image.",
		RequiredPerm: "profile.edit_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["avatar_url"],
			"properties":{
				"avatar_url":{"type":"string","format":"uri","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AvatarURL string `json:"avatar_url"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "utilisateur non authentifié"}, nil
			}
			if _, err := deps.DB.ExecContext(ctx, `
				INSERT INTO user_avatars(user_id, avatar_url) VALUES(?,?)
				ON CONFLICT(user_id) DO UPDATE SET
				  avatar_url = excluded.avatar_url,
				  uploaded_at = CURRENT_TIMESTAMP
			`, u.ID, p.AvatarURL); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Avatar mis à jour.", Data: map[string]string{"avatar_url": p.AvatarURL}}, nil
		},
	})
}
