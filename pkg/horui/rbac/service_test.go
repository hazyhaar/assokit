// CLAUDE:SUMMARY Tests gardiens Service RBAC-2 : Can hot-path, Recompute atomique, closure DAG, hooks mutation, cycle-proof (M-ASSOKIT-RBAC-2).
package rbac

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
)

func openRBACTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	return db
}

func newService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := openRBACTestDB(t)
	svc := &Service{
		Store: &Store{DB: db},
		Cache: &Cache{},
	}
	return svc, db
}

// setupDAG crée A inherits B inherits C avec perms, retourne (gradeA, gradeB, gradeC, permA, permB, permC).
func setupDAG(ctx context.Context, t *testing.T, store *Store) (gA, gB, gC, pA, pB, pC string) {
	t.Helper()
	var err error
	gA, err = store.CreateGrade(ctx, "grade-A")
	if err != nil {
		t.Fatalf("create grade A: %v", err)
	}
	gB, err = store.CreateGrade(ctx, "grade-B")
	if err != nil {
		t.Fatalf("create grade B: %v", err)
	}
	gC, err = store.CreateGrade(ctx, "grade-C")
	if err != nil {
		t.Fatalf("create grade C: %v", err)
	}
	pA, err = store.EnsurePermission(ctx, "perm.a", "")
	if err != nil {
		t.Fatalf("perm A: %v", err)
	}
	pB, err = store.EnsurePermission(ctx, "perm.b", "")
	if err != nil {
		t.Fatalf("perm B: %v", err)
	}
	pC, err = store.EnsurePermission(ctx, "perm.c", "")
	if err != nil {
		t.Fatalf("perm C: %v", err)
	}
	// A → B → C
	if err := store.AddInherit(ctx, gA, gB); err != nil {
		t.Fatalf("A inherits B: %v", err)
	}
	if err := store.AddInherit(ctx, gB, gC); err != nil {
		t.Fatalf("B inherits C: %v", err)
	}
	// Perms directes
	if err := store.GrantPerm(ctx, gA, pA); err != nil {
		t.Fatalf("grant perm.a to A: %v", err)
	}
	if err := store.GrantPerm(ctx, gB, pB); err != nil {
		t.Fatalf("grant perm.b to B: %v", err)
	}
	if err := store.GrantPerm(ctx, gC, pC); err != nil {
		t.Fatalf("grant perm.c to C: %v", err)
	}
	return
}

// TestService_AssignGradeTriggersRecompute : AssignGrade → Can() retourne la perm du grade.
func TestService_AssignGradeTriggersRecompute(t *testing.T) {
	svc, db := newService(t)
	defer db.Close()
	ctx := context.Background()

	gradeID, err := svc.Store.CreateGrade(ctx, "admin-grade")
	if err != nil {
		t.Fatalf("create grade: %v", err)
	}
	permID, err := svc.Store.EnsurePermission(ctx, "feedback.triage", "")
	if err != nil {
		t.Fatalf("ensure perm: %v", err)
	}
	if err := svc.Store.GrantPerm(ctx, gradeID, permID); err != nil {
		t.Fatalf("grant perm: %v", err)
	}

	// Before AssignGrade → Can = false
	ok, err := svc.Can(ctx, "user-1", "feedback.triage")
	if err != nil {
		t.Fatalf("Can before: %v", err)
	}
	if ok {
		t.Error("Can before AssignGrade should be false")
	}

	// AssignGrade déclenche Recompute
	if err := svc.AssignGrade(ctx, "user-1", gradeID); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}

	// Can doit maintenant retourner true
	ok, err = svc.Can(ctx, "user-1", "feedback.triage")
	if err != nil {
		t.Fatalf("Can after: %v", err)
	}
	if !ok {
		t.Error("Can after AssignGrade should be true")
	}
}

// TestService_TransitiveClosure_DAG : grade A inherits B inherits C → user avec A a perm.a + perm.b + perm.c.
func TestService_TransitiveClosure_DAG(t *testing.T) {
	svc, db := newService(t)
	defer db.Close()
	ctx := context.Background()

	gA, _, _, _, _, _ := setupDAG(ctx, t, svc.Store)

	if err := svc.AssignGrade(ctx, "user-dag", gA); err != nil {
		t.Fatalf("AssignGrade A: %v", err)
	}

	for _, perm := range []string{"perm.a", "perm.b", "perm.c"} {
		ok, err := svc.Can(ctx, "user-dag", perm)
		if err != nil {
			t.Fatalf("Can(%s): %v", perm, err)
		}
		if !ok {
			t.Errorf("user avec grade A devrait avoir %s (closure transitive)", perm)
		}
	}
}

// TestService_CycleProof_NoInfiniteLoop : RBAC-1 empêche les cycles, Recompute ne boucle pas.
func TestService_CycleProof_NoInfiniteLoop(t *testing.T) {
	svc, db := newService(t)
	defer db.Close()
	ctx := context.Background()

	gA, err := svc.Store.CreateGrade(ctx, "grade-cycle-a")
	if err != nil {
		t.Fatalf("create grade A: %v", err)
	}
	gB, err := svc.Store.CreateGrade(ctx, "grade-cycle-b")
	if err != nil {
		t.Fatalf("create grade B: %v", err)
	}
	if err := svc.Store.AddInherit(ctx, gA, gB); err != nil {
		t.Fatalf("A inherits B: %v", err)
	}
	// Tentative de cycle B → A doit être bloquée par RBAC-1
	err = svc.Store.AddInherit(ctx, gB, gA)
	if err == nil {
		t.Fatal("cycle B→A aurait dû être refusé par ErrCycleDetected")
	}
	// Assigner A à un user et recomputer doit terminer sans boucle infinie
	if err := svc.AssignGrade(ctx, "user-cycle", gA); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}
}

// TestCan_HotPath_NoDBQueryAfterCacheHit : après un 1er Can(), le 2ème ne touche pas la DB.
// Vérification par "poison" de la DB (DROP table) après chargement du cache.
func TestCan_HotPath_NoDBQueryAfterCacheHit(t *testing.T) {
	svc, db := newService(t)
	defer db.Close()
	ctx := context.Background()

	gID, err := svc.Store.CreateGrade(ctx, "grade-hotpath")
	if err != nil {
		t.Fatalf("create grade: %v", err)
	}
	pID, err := svc.Store.EnsurePermission(ctx, "hot.perm", "")
	if err != nil {
		t.Fatalf("ensure perm: %v", err)
	}
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.AssignGrade(ctx, "user-hot", gID) //nolint:errcheck

	// Premier Can() → charge L2 et peuple L1
	ok, err := svc.Can(ctx, "user-hot", "hot.perm")
	if err != nil || !ok {
		t.Fatalf("Can warm: ok=%v err=%v", ok, err)
	}

	// "Poison" la DB : DROP la table de jointure — si Can() touche la DB, il échouera
	if _, err := db.Exec(`DROP TABLE user_effective_permissions`); err != nil {
		t.Fatalf("DROP table: %v", err)
	}

	// Deuxième Can() doit réussir depuis L1 sans toucher la DB
	ok, err = svc.Can(ctx, "user-hot", "hot.perm")
	if err != nil {
		t.Errorf("Can hot-path: unexpected error after cache warm: %v", err)
	}
	if !ok {
		t.Error("Can hot-path: should still return true from L1 cache")
	}
}

// TestService_RemoveGradeRecomputes : RemoveGrade → perm perdue.
func TestService_RemoveGradeRecomputes(t *testing.T) {
	svc, db := newService(t)
	defer db.Close()
	ctx := context.Background()

	gID, _ := svc.Store.CreateGrade(ctx, "grade-remove")
	pID, _ := svc.Store.EnsurePermission(ctx, "removable.perm", "")
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.AssignGrade(ctx, "user-rem", gID) //nolint:errcheck

	ok, _ := svc.Can(ctx, "user-rem", "removable.perm")
	if !ok {
		t.Fatal("should have perm before remove")
	}

	if err := svc.RemoveGrade(ctx, "user-rem", gID); err != nil {
		t.Fatalf("RemoveGrade: %v", err)
	}
	ok, err := svc.Can(ctx, "user-rem", "removable.perm")
	if err != nil {
		t.Fatalf("Can after remove: %v", err)
	}
	if ok {
		t.Error("should not have perm after RemoveGrade")
	}
}

// BenchmarkCan_CacheHit : hot path L1 doit être < 1µs.
func BenchmarkCan_CacheHit(b *testing.B) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	svc := &Service{Store: &Store{DB: db}, Cache: &Cache{}}
	ctx := context.Background()

	gID, _ := svc.Store.CreateGrade(ctx, "bench-grade")
	pID, _ := svc.Store.EnsurePermission(ctx, "bench.perm", "")
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.AssignGrade(ctx, "bench-user", gID) //nolint:errcheck

	// Warm cache
	svc.Can(ctx, "bench-user", "bench.perm") //nolint:errcheck

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		svc.Can(ctx, "bench-user", "bench.perm") //nolint:errcheck
	}
}
