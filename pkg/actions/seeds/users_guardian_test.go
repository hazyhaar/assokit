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

// Strate S5 — tests gardiens comportementaux des mutations users.* et
// forum.post.delete. But : prouver que chaque action MUTE réellement la base.
// Les helpers actionByID / mustExec / count sont définis dans account_test.go
// (même package seeds_test) et réutilisés tels quels.

// newGuardianDB ouvre une DB SQLite en mémoire migrée par chassis.Run.
func newGuardianDB(t *testing.T) *sql.DB {
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
	return db
}

func runGuardian(t *testing.T, db *sql.DB, id, params string) {
	t.Helper()
	act := actionByID(t, id)
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(params))
	if err != nil || res.Status != "ok" {
		t.Fatalf("%s : status=%s message=%q err=%v — FAUX OK ou erreur DB", id, res.Status, res.Message, err)
	}
}

// TestUsersDeactivate_Guardian : l'action doit passer is_active à 0 en base.
func TestUsersDeactivate_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u1','u1@example.org','x','U1',1)`)

	runGuardian(t, db, "users.deactivate", `{"uid":"u1","reason":"abus"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND is_active=0`); n != 1 {
		t.Fatalf("users.deactivate n'a pas mis is_active=0 (n=%d) — FAUX OK / mauvaise colonne", n)
	}
}

// TestUsersReactivate_Guardian : l'action doit repasser is_active à 1.
func TestUsersReactivate_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u1','u1@example.org','x','U1',0)`)

	runGuardian(t, db, "users.reactivate", `{"uid":"u1"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND is_active=1`); n != 1 {
		t.Fatalf("users.reactivate n'a pas remis is_active=1 (n=%d) — FAUX OK / mauvaise colonne", n)
	}
}

// TestUsersRoleAssign_Guardian : l'action doit créer une ligne d'affectation de
// grade pour l'utilisateur. (Le schéma réel matérialise les grades dans
// grades + user_grades, FK sur grades(id).)
func TestUsersRoleAssign_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	// grade 'sys-admin' déjà semé par la migration RBAC (FK user_grades.grade_id).

	runGuardian(t, db, "users.role_assign", `{"uid":"u1","grade_id":"sys-admin"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM user_grades WHERE user_id='u1' AND grade_id='sys-admin'`); n != 1 {
		t.Fatalf("users.role_assign n'a pas créé d'affectation (n=%d) — FAUX OK", n)
	}
}

// TestUsersRoleRemove_Guardian : l'action doit supprimer la ligne d'affectation.
func TestUsersRoleRemove_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO user_grades(user_id,grade_id) VALUES('u1','sys-admin')`)

	runGuardian(t, db, "users.role_remove", `{"uid":"u1","grade_id":"sys-admin"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM user_grades WHERE user_id='u1' AND grade_id='sys-admin'`); n != 0 {
		t.Fatalf("users.role_remove n'a pas supprimé l'affectation (n=%d) — FAUX OK", n)
	}
}

// TestUsersDelete_Guardian : l'action doit réellement supprimer l'utilisateur.
func TestUsersDelete_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)

	runGuardian(t, db, "users.delete", `{"uid":"u1"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1'`); n != 0 {
		t.Fatalf("users.delete n'a pas supprimé l'utilisateur (n=%d) — FAUX OK", n)
	}
}

// TestForumPostDelete_Guardian : l'action modérateur doit faire disparaître le
// nœud de la base (l'implémentation réelle fait un hard DELETE).
func TestForumPostDelete_Guardian(t *testing.T) {
	db := newGuardianDB(t)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title) VALUES('n1','s1','post','T')`)

	runGuardian(t, db, "forum.post.delete", `{"id":"n1","reason":"spam"}`)

	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='n1'`); n != 0 {
		t.Fatalf("forum.post.delete n'a pas supprimé le nœud (n=%d) — FAUX OK", n)
	}
}
