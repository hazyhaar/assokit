package seeds

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func initAccount(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "account.delete_self",
		Title:        "Supprimer son compte (RGPD)",
		Description:  "Supprime définitivement le compte de l'utilisateur courant conformément au RGPD : anonymise ses contributions et efface ses données personnelles.",
		RequiredPerm: "account.delete_self",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["confirm"],
			"properties":{
				"confirm":{"type":"string","const":"DELETE_MY_ACCOUNT"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Confirm string `json:"confirm"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if p.Confirm != "DELETE_MY_ACCOUNT" {
				return actions.Result{Status: "error", Message: "confirmation requise"}, nil
			}
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			if err := deleteUserRGPD(ctx, deps.DB, u.ID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Compte supprimé conformément au RGPD."}, nil
		},
	})
}

// deleteUserRGPD efface définitivement un utilisateur dans une transaction :
//  1. anonymise les références nullables non-cascade (contributions conservées,
//     mais détachées de l'identité) ;
//  2. supprime les références NOT NULL non-cascade (actions de modération émises) ;
//  3. supprime la ligne users → les FK ON DELETE CASCADE nettoient le reste
//     (rôles, messages, adhésions, tokens, avatars, warnings/timeouts reçus…).
func deleteUserRGPD(ctx context.Context, db *sql.DB, userID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account.delete_self: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 1. Anonymise les références nullables (sinon RESTRICT bloque la suppression).
	anonymize := []string{
		`UPDATE nodes SET author_id = NULL WHERE author_id = ?`,
		`UPDATE login_magic_tokens SET user_id = NULL WHERE user_id = ?`,
		`UPDATE branding_kv SET updated_by = NULL WHERE updated_by = ?`,
		`UPDATE connectors SET configured_by = NULL WHERE configured_by = ?`,
		`UPDATE connector_credentials SET set_by = NULL WHERE set_by = ?`,
		`UPDATE donations SET user_id = NULL WHERE user_id = ?`,
	}
	for _, q := range anonymize {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return fmt.Errorf("account.delete_self: anonymise: %w", err)
		}
	}

	// 2. Références NOT NULL non-cascade (modération émise par l'utilisateur) :
	//    impossible de nullifier → on supprime ces lignes.
	purge := []string{
		`DELETE FROM forum_warnings WHERE issued_by = ?`,
		`DELETE FROM forum_timeouts WHERE issued_by = ?`,
	}
	for _, q := range purge {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return fmt.Errorf("account.delete_self: purge: %w", err)
		}
	}

	// 3. Suppression de l'utilisateur → cascade le reste.
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("account.delete_self: delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("account.delete_self: utilisateur introuvable")
	}
	return tx.Commit()
}
