// CLAUDE:SUMMARY Tests gardiens RBAC cache race + cascade orphan delete grade (M-ASSOKIT-AUDIT-FIX-2).
package rbac_test

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

// TestService_GradeDeleteCascadeOrphans : delete grade → user perd ses permissions
// (CASCADE sur user_grades) + cache invalidé après Recompute.
func TestService_GradeDeleteCascadeOrphans(t *testing.T) {
	db := openRBACDeepDB(t)
	store := &rbac.Store{DB: db}
	cache := &rbac.Cache{}
	svc := &rbac.Service{Store: store, Cache: cache}
	ctx := context.Background()

	gid, err := store.CreateGrade(ctx, "GradeOrphan")
	if err != nil {
		t.Fatalf("CreateGrade: %v", err)
	}
	pid, err := store.EnsurePermission(ctx, "perm.cascade", "Cascade test")
	if err != nil {
		t.Fatalf("EnsurePermission: %v", err)
	}
	if err := store.GrantPerm(ctx, gid, pid); err != nil {
		t.Fatalf("GrantPerm: %v", err)
	}
	if err := svc.AssignGrade(ctx, "user-cascade", gid); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}

	if ok, _ := svc.Can(ctx, "user-cascade", "perm.cascade"); !ok {
		t.Fatal("avant delete : user devrait avoir perm.cascade")
	}

	if err := store.DeleteGrade(ctx, gid); err != nil {
		t.Fatalf("DeleteGrade: %v", err)
	}

	// Vérifier user_grades cascade
	var ugCount int
	db.QueryRow(`SELECT COUNT(*) FROM user_grades WHERE user_id='user-cascade'`).Scan(&ugCount)
	if ugCount != 0 {
		t.Errorf("CASCADE user_grades : attendu 0, got %d", ugCount)
	}

	// Recompute → user n'a plus aucune perm.
	if err := svc.Recompute(ctx, "user-cascade"); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	if ok, _ := svc.Can(ctx, "user-cascade", "perm.cascade"); ok {
		t.Error("après delete grade + recompute : user a toujours perm.cascade (cache stale ?)")
	}
}
