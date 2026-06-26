package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/rbac"
)

// rbacService retourne le Service RBAC applicatif partagé (deps.RBAC) — celui qui
// sert Can(), donc le seul dont l'invalidation de cache compte. Fallback sur un
// Service transitoire si deps.RBAC est nil (montage de test minimal) : le recalcul
// des permissions effectives en base reste correct, seule l'invalidation du cache
// vivant manque alors (acceptable hors prod).
func rbacService(deps app.AppDeps) *rbac.Service {
	if deps.RBAC != nil {
		return deps.RBAC
	}
	return &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}
}

func initRBAC(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.create",
		Title:        "Créer un grade RBAC",
		Description:  "Crée un nouveau grade RBAC avec un nom.",
		RequiredPerm: "rbac.grades.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["name"],
			"properties":{
				"name":{"type":"string","minLength":1,"maxLength":64},
				"desc":{"type":"string","maxLength":256}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Name string `json:"name"`
				Desc string `json:"desc"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &rbac.Store{DB: deps.DB}
			id, err := store.CreateGrade(ctx, p.Name)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Grade créé.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.delete",
		Title:        "Supprimer un grade RBAC",
		Description:  "Supprime un grade RBAC non-système.",
		RequiredPerm: "rbac.grades.write",
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
			store := &rbac.Store{DB: deps.DB}
			if err := store.DeleteGrade(ctx, p.ID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Grade supprimé."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.grant_perm",
		Title:        "Accorder une permission à un grade",
		Description:  "Accorde une permission atomique à un grade RBAC.",
		RequiredPerm: "rbac.grades.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["grade_id","perm_id"],
			"properties":{
				"grade_id":{"type":"string","minLength":1},
				"perm_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				GradeID string `json:"grade_id"`
				PermID  string `json:"perm_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := rbacService(deps).GrantPerm(ctx, p.GradeID, p.PermID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Permission accordée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.revoke_perm",
		Title:        "Révoquer une permission d'un grade",
		Description:  "Révoque une permission atomique d'un grade RBAC.",
		RequiredPerm: "rbac.grades.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["grade_id","perm_id"],
			"properties":{
				"grade_id":{"type":"string","minLength":1},
				"perm_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				GradeID string `json:"grade_id"`
				PermID  string `json:"perm_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := rbacService(deps).RevokePerm(ctx, p.GradeID, p.PermID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Permission révoquée."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.add_inherit",
		Title:        "Ajouter héritage de grade",
		Description:  "Fait hériter un grade d'un grade parent (vérification anti-cycle).",
		RequiredPerm: "rbac.grades.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["grade_id","parent_id"],
			"properties":{
				"grade_id":{"type":"string","minLength":1},
				"parent_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				GradeID  string `json:"grade_id"`
				ParentID string `json:"parent_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := rbacService(deps).AddInherit(ctx, p.GradeID, p.ParentID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Héritage ajouté."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "rbac.grade.remove_inherit",
		Title:        "Supprimer héritage de grade",
		Description:  "Supprime la relation d'héritage entre deux grades.",
		RequiredPerm: "rbac.grades.write",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["grade_id","parent_id"],
			"properties":{
				"grade_id":{"type":"string","minLength":1},
				"parent_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				GradeID  string `json:"grade_id"`
				ParentID string `json:"parent_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			if err := rbacService(deps).RemoveInherit(ctx, p.GradeID, p.ParentID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Héritage supprimé."}, nil
		},
	})
}
