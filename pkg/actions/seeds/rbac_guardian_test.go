package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
)

// rbacDB ouvre une base neuve, applique le chassis (qui seed les grades
// système sys-admin/sys-moderator/sys-member/sys-guest) et retourne deps + ctx.
func rbacDB(t *testing.T) (*sql.DB, app.AppDeps, context.Context) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	return db, app.AppDeps{DB: db}, context.Background()
}

// createGrade exécute l'action rbac.grade.create et retourne l'ID du grade créé.
func createGrade(t *testing.T, deps app.AppDeps, ctx context.Context, name string) string {
	t.Helper()
	act := actionByID(t, "rbac.grade.create")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"name":"`+name+`"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("create %q: status=%s err=%v", name, res.Status, err)
	}
	data, ok := res.Data.(map[string]string)
	if !ok || data["id"] == "" {
		t.Fatalf("create %q: pas d'ID retourné (data=%v)", name, res.Data)
	}
	return data["id"]
}

// TestRBACGradeCreate_ReallyInserts : rbac.grade.create doit insérer une ligne
// dans grades, pas seulement répondre "ok".
func TestRBACGradeCreate_ReallyInserts(t *testing.T) {
	db, deps, ctx := rbacDB(t)
	before := count(t, db, `SELECT COUNT(*) FROM grades`)

	id := createGrade(t, deps, ctx, "Bénévole")

	after := count(t, db, `SELECT COUNT(*) FROM grades`)
	if after != before+1 {
		t.Fatalf("grades: %d -> %d, attendu +1 — FAUX OK", before, after)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grades WHERE name='Bénévole' AND system=0 AND id='`+id+`'`); n != 1 {
		t.Fatalf("grade nommé absent (n=%d)", n)
	}
}

// TestRBACGradeGrantRevokePerm_ReallyMutate : grant_perm insère dans
// grade_permissions, revoke_perm l'efface.
func TestRBACGradeGrantRevokePerm_ReallyMutate(t *testing.T) {
	db, deps, ctx := rbacDB(t)
	gradeID := createGrade(t, deps, ctx, "Trésorier")
	mustExec(t, db, `INSERT INTO permissions(id,name,description) VALUES('perm1','finance.read','')`)

	grant := actionByID(t, "rbac.grade.grant_perm")
	res, err := grant.Run(ctx, deps, json.RawMessage(`{"grade_id":"`+gradeID+`","perm_id":"perm1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("grant_perm: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grade_permissions WHERE grade_id='`+gradeID+`' AND permission_id='perm1'`); n != 1 {
		t.Fatalf("grant_perm n'a pas inséré (n=%d) — FAUX OK", n)
	}

	revoke := actionByID(t, "rbac.grade.revoke_perm")
	res, err = revoke.Run(ctx, deps, json.RawMessage(`{"grade_id":"`+gradeID+`","perm_id":"perm1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("revoke_perm: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grade_permissions WHERE grade_id='`+gradeID+`' AND permission_id='perm1'`); n != 0 {
		t.Fatalf("revoke_perm n'a pas effacé (n=%d) — FAUX OK", n)
	}
}

// TestRBACGradeAddRemoveInherit_ReallyMutate : add_inherit insère dans
// grade_inherits, remove_inherit l'efface.
func TestRBACGradeAddRemoveInherit_ReallyMutate(t *testing.T) {
	db, deps, ctx := rbacDB(t)
	childID := createGrade(t, deps, ctx, "Adjoint")
	parentID := createGrade(t, deps, ctx, "Responsable")

	add := actionByID(t, "rbac.grade.add_inherit")
	res, err := add.Run(ctx, deps, json.RawMessage(`{"grade_id":"`+childID+`","parent_id":"`+parentID+`"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("add_inherit: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grade_inherits WHERE child_id='`+childID+`' AND parent_id='`+parentID+`'`); n != 1 {
		t.Fatalf("add_inherit n'a pas inséré (n=%d) — FAUX OK", n)
	}

	rem := actionByID(t, "rbac.grade.remove_inherit")
	res, err = rem.Run(ctx, deps, json.RawMessage(`{"grade_id":"`+childID+`","parent_id":"`+parentID+`"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("remove_inherit: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grade_inherits WHERE child_id='`+childID+`' AND parent_id='`+parentID+`'`); n != 0 {
		t.Fatalf("remove_inherit n'a pas effacé (n=%d) — FAUX OK", n)
	}
}

// TestRBACGradeDelete_ReallyDeletes : rbac.grade.delete efface le grade créé,
// le COUNT revient à l'état initial.
func TestRBACGradeDelete_ReallyDeletes(t *testing.T) {
	db, deps, ctx := rbacDB(t)
	before := count(t, db, `SELECT COUNT(*) FROM grades`)
	id := createGrade(t, deps, ctx, "Éphémère")
	if count(t, db, `SELECT COUNT(*) FROM grades`) != before+1 {
		t.Fatalf("précondition: grade non créé")
	}

	del := actionByID(t, "rbac.grade.delete")
	res, err := del.Run(ctx, deps, json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("delete: status=%s err=%v", res.Status, err)
	}
	if after := count(t, db, `SELECT COUNT(*) FROM grades`); after != before {
		t.Fatalf("grades: attendu retour à %d, obtenu %d — FAUX OK", before, after)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grades WHERE id='`+id+`'`); n != 0 {
		t.Fatalf("grade non supprimé (n=%d)", n)
	}
}

// TestRBACGradeDelete_SystemGradeRefused : INVARIANT ESCALADE. La suppression
// d'un grade système ('sys-admin') doit échouer (Result.Status == "error") et
// laisser le grade en place. Le Store retourne ErrSystemGrade ; l'action
// remonte donc une Result error.
func TestRBACGradeDelete_SystemGradeRefused(t *testing.T) {
	db, deps, ctx := rbacDB(t)
	if n := count(t, db, `SELECT COUNT(*) FROM grades WHERE id='sys-admin' AND system=1`); n != 1 {
		t.Fatalf("précondition: grade système sys-admin absent (n=%d)", n)
	}

	del := actionByID(t, "rbac.grade.delete")
	res, _ := del.Run(ctx, deps, json.RawMessage(`{"id":"sys-admin"}`))
	if res.Status != "error" {
		t.Fatalf("escalade: suppression sys-admin status=%s, attendu error", res.Status)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grades WHERE id='sys-admin'`); n != 1 {
		t.Fatalf("escalade: grade système supprimé malgré le refus (n=%d) — TROU DE SÉCURITÉ", n)
	}
}
