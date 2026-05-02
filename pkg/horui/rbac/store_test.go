package rbac_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnsurePermission_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	id1, err := s.EnsurePermission(ctx, "post:create", "")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	id2, err := s.EnsurePermission(ctx, "post:create", "")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("EnsurePermission non idempotent : %q != %q", id1, id2)
	}
}

func TestCreateDeleteGrade(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	id, err := s.CreateGrade(ctx, "moderateur")
	if err != nil {
		t.Fatalf("CreateGrade: %v", err)
	}
	g, err := s.GetGrade(ctx, id)
	if err != nil {
		t.Fatalf("GetGrade: %v", err)
	}
	if g.Name != "moderateur" {
		t.Errorf("nom grade incorrect: %q", g.Name)
	}
	if err := s.DeleteGrade(ctx, id); err != nil {
		t.Fatalf("DeleteGrade: %v", err)
	}
	_, err = s.GetGrade(ctx, id)
	if err != rbac.ErrNotFound {
		t.Errorf("après suppression, attendu ErrNotFound, got %v", err)
	}
}

func TestDeleteSystemGradeForbidden(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	// Insérer directement un grade système.
	var sysID string
	row := db.QueryRowContext(ctx,
		`INSERT INTO grades(id, name, system) VALUES('sys-1', 'admin', 1) RETURNING id`)
	if err := row.Scan(&sysID); err != nil {
		t.Fatalf("insert system grade: %v", err)
	}
	if err := s.DeleteGrade(ctx, sysID); err != rbac.ErrSystemGrade {
		t.Errorf("attendu ErrSystemGrade, got %v", err)
	}
}

func TestGrantRevokePerm(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	gID, _ := s.CreateGrade(ctx, "editeur")
	pID, _ := s.EnsurePermission(ctx, "article:write", "")

	if err := s.GrantPerm(ctx, gID, pID); err != nil {
		t.Fatalf("GrantPerm: %v", err)
	}
	perms, err := s.GradePermissions(ctx, gID)
	if err != nil {
		t.Fatalf("GradePermissions: %v", err)
	}
	if len(perms) != 1 || perms[0].ID != pID {
		t.Errorf("permission non accordée correctement: %+v", perms)
	}

	if err := s.RevokePerm(ctx, gID, pID); err != nil {
		t.Fatalf("RevokePerm: %v", err)
	}
	perms, _ = s.GradePermissions(ctx, gID)
	if len(perms) != 0 {
		t.Errorf("permission non révoquée: %+v", perms)
	}
}

func TestAddInherit_CycleDetected(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	a, _ := s.CreateGrade(ctx, "a")
	b, _ := s.CreateGrade(ctx, "b")
	c, _ := s.CreateGrade(ctx, "c")

	// a → b → c
	if err := s.AddInherit(ctx, a, b); err != nil {
		t.Fatalf("AddInherit a→b: %v", err)
	}
	if err := s.AddInherit(ctx, b, c); err != nil {
		t.Fatalf("AddInherit b→c: %v", err)
	}
	// c → a créerait un cycle
	if err := s.AddInherit(ctx, c, a); err != rbac.ErrCycleDetected {
		t.Errorf("AddInherit c→a : attendu ErrCycleDetected, got %v", err)
	}
}

func TestAssignRemoveGrade(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	gID, _ := s.CreateGrade(ctx, "lecteur")
	const userID = "user-abc"

	if err := s.AssignGrade(ctx, userID, gID); err != nil {
		t.Fatalf("AssignGrade: %v", err)
	}
	grades, err := s.UserGrades(ctx, userID)
	if err != nil {
		t.Fatalf("UserGrades: %v", err)
	}
	if len(grades) != 1 || grades[0].ID != gID {
		t.Errorf("grade non assigné: %+v", grades)
	}

	if err := s.RemoveGrade(ctx, userID, gID); err != nil {
		t.Fatalf("RemoveGrade: %v", err)
	}
	grades, _ = s.UserGrades(ctx, userID)
	if len(grades) != 0 {
		t.Errorf("grade non retiré: %+v", grades)
	}
}

func TestAuditTrail_Populated(t *testing.T) {
	db := openTestDB(t)
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	gID, _ := s.CreateGrade(ctx, "testeur")
	pID, _ := s.EnsurePermission(ctx, "test:read", "")
	s.GrantPerm(ctx, gID, pID)
	s.AssignGrade(ctx, "user-x", gID)

	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rbac_audit`).Scan(&count)
	if count < 3 {
		t.Errorf("audit trail insuffisant : %d entrées (attendu >= 3)", count)
	}
}

// TestStore_GradeDescendants_RecursiveCTE : GradeDescendants retourne la closure descendante.
func TestStore_GradeDescendants_RecursiveCTE(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	s := &rbac.Store{DB: db}
	ctx := context.Background()

	// Setup : A inherits B inherits C
	gA, _ := s.CreateGrade(ctx, "desc-grade-a")
	gB, _ := s.CreateGrade(ctx, "desc-grade-b")
	gC, _ := s.CreateGrade(ctx, "desc-grade-c")
	s.AddInherit(ctx, gA, gB) //nolint:errcheck
	s.AddInherit(ctx, gB, gC) //nolint:errcheck

	// Descendants de C = {B, A}
	desc, err := s.GradeDescendants(ctx, gC)
	if err != nil {
		t.Fatalf("GradeDescendants C: %v", err)
	}
	m := make(map[string]bool, len(desc))
	for _, id := range desc {
		m[id] = true
	}
	if !m[gA] {
		t.Errorf("GradeDescendants(C) doit contenir A")
	}
	if !m[gB] {
		t.Errorf("GradeDescendants(C) doit contenir B")
	}

	// Descendants de B = {A} seulement
	descB, err := s.GradeDescendants(ctx, gB)
	if err != nil {
		t.Fatalf("GradeDescendants B: %v", err)
	}
	if len(descB) != 1 || descB[0] != gA {
		t.Errorf("GradeDescendants(B) = %v, want [%s]", descB, gA)
	}

	// Descendants de A = [] (feuille)
	descA, err := s.GradeDescendants(ctx, gA)
	if err != nil {
		t.Fatalf("GradeDescendants A: %v", err)
	}
	if len(descA) != 0 {
		t.Errorf("GradeDescendants(A) doit être vide, got %v", descA)
	}
}
