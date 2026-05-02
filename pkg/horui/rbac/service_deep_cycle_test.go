// CLAUDE:SUMMARY Tests gardiens RBAC deep cycle + cache race (M-ASSOKIT-AUDIT-FIX-2).
package rbac_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

// rbacTestDB schéma minimal pour tests RBAC.
const rbacTestSchema = `
CREATE TABLE permissions (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE TABLE grades (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, system INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE TABLE grade_permissions (
	grade_id TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
	permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
	PRIMARY KEY(grade_id, permission_id)
) STRICT;
CREATE TABLE grade_inherits (
	child_id TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
	parent_id TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
	PRIMARY KEY(child_id, parent_id)
) STRICT;
CREATE TABLE user_grades (
	user_id TEXT NOT NULL,
	grade_id TEXT NOT NULL REFERENCES grades(id) ON DELETE CASCADE,
	PRIMARY KEY(user_id, grade_id)
) STRICT;
CREATE TABLE user_effective_permissions (
	user_id TEXT NOT NULL,
	permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
	PRIMARY KEY(user_id, permission_id)
) STRICT;
CREATE TABLE rbac_audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	action TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '',
	target_grade_id TEXT, details TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
) STRICT;
`

func openRBACDeepDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(rbacTestSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestService_DeepCycleDetection_4Levels : A→B→C→D, AddInherit(D,A) → ErrCycleDetected.
func TestService_DeepCycleDetection_4Levels(t *testing.T) {
	db := openRBACDeepDB(t)
	store := &rbac.Store{DB: db}
	ctx := context.Background()

	idA, err := store.CreateGrade(ctx, "A")
	if err != nil {
		t.Fatalf("createGrade A: %v", err)
	}
	idB, _ := store.CreateGrade(ctx, "B")
	idC, _ := store.CreateGrade(ctx, "C")
	idD, _ := store.CreateGrade(ctx, "D")

	// Chaîne A→B→C→D (D hérite de C, C hérite de B, B hérite de A).
	if err := store.AddInherit(ctx, idB, idA); err != nil {
		t.Fatalf("B inherit A: %v", err)
	}
	if err := store.AddInherit(ctx, idC, idB); err != nil {
		t.Fatalf("C inherit B: %v", err)
	}
	if err := store.AddInherit(ctx, idD, idC); err != nil {
		t.Fatalf("D inherit C: %v", err)
	}

	// Tentative de fermer le cycle : A→D (A hérite de D, qui descend de A).
	err = store.AddInherit(ctx, idA, idD)
	if err != rbac.ErrCycleDetected {
		t.Errorf("AddInherit(A,D) = %v, attendu ErrCycleDetected", err)
	}
}

// TestService_DeepCycleDetection_5Levels : 5-level chain, fermeture cycle bloquée.
func TestService_DeepCycleDetection_5Levels(t *testing.T) {
	db := openRBACDeepDB(t)
	store := &rbac.Store{DB: db}
	ctx := context.Background()

	ids := make([]string, 5)
	for i, name := range []string{"L1", "L2", "L3", "L4", "L5"} {
		id, err := store.CreateGrade(ctx, name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		ids[i] = id
	}
	for i := 1; i < 5; i++ {
		if err := store.AddInherit(ctx, ids[i], ids[i-1]); err != nil {
			t.Fatalf("inherit L%d→L%d: %v", i+1, i, err)
		}
	}

	// L1 inherit L5 → cycle 5-level.
	if err := store.AddInherit(ctx, ids[0], ids[4]); err != rbac.ErrCycleDetected {
		t.Errorf("AddInherit(L1,L5) = %v, attendu ErrCycleDetected", err)
	}
}

// TestService_CacheInvalidationRaceCondition : GrantPerm + Recompute parallèles → état cohérent.
func TestService_CacheInvalidationRaceCondition(t *testing.T) {
	db := openRBACDeepDB(t)
	store := &rbac.Store{DB: db}
	cache := &rbac.Cache{}
	svc := &rbac.Service{Store: store, Cache: cache}
	ctx := context.Background()

	// Setup : grade G + user U assigné.
	gid, _ := store.CreateGrade(ctx, "G")
	pid, _ := store.EnsurePermission(ctx, "perm.race", "Race test")
	if err := store.AssignGrade(ctx, "user-race", gid); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}

	// Concurrent : 10 goroutines GrantPerm + 10 Recompute.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = svc.GrantPerm(ctx, gid, pid)
		}()
		go func() {
			defer wg.Done()
			_ = svc.Recompute(ctx, "user-race")
		}()
	}
	wg.Wait()

	// État final : la perm doit être effective pour user-race.
	allowed, err := svc.Can(ctx, "user-race", "perm.race")
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if !allowed {
		t.Errorf("Can(user-race, perm.race) = false après concurrent GrantPerm+Recompute, attendu true")
	}
}

// TestService_GrantRevokeCacheConsistent : grant→revoke → Can reflète immédiatement.
func TestService_GrantRevokeCacheConsistent(t *testing.T) {
	db := openRBACDeepDB(t)
	store := &rbac.Store{DB: db}
	svc := &rbac.Service{Store: store, Cache: &rbac.Cache{}}
	ctx := context.Background()

	gid, _ := store.CreateGrade(ctx, "G2")
	pid, _ := store.EnsurePermission(ctx, "perm.toggle", "")
	store.AssignGrade(ctx, "user-toggle", gid)

	if err := svc.GrantPerm(ctx, gid, pid); err != nil {
		t.Fatalf("GrantPerm: %v", err)
	}
	if ok, _ := svc.Can(ctx, "user-toggle", "perm.toggle"); !ok {
		t.Error("Can post-Grant = false, attendu true")
	}

	if err := svc.RevokePerm(ctx, gid, pid); err != nil {
		t.Fatalf("RevokePerm: %v", err)
	}
	if ok, _ := svc.Can(ctx, "user-toggle", "perm.toggle"); ok {
		t.Error("Can post-Revoke = true, attendu false (cache stale ?)")
	}
}
