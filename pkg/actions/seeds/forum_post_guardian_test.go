package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// newForumDB ouvre une DB neuve en mémoire, applique le schéma complet (chassis.Run,
// qui inclut la migration 00022 ajoutant pinned/locked), et amorce un thread forum
// auquel rattacher les posts. Insère roles AVANT users (FK).
func newForumDB(t *testing.T) (*sql.DB, string) {
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
	// Rôle puis utilisateur (ordre des FK).
	mustExec(t, db, `INSERT INTO roles(id,label) VALUES('r1','Membre')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO user_roles(user_id,role_id) VALUES('u1','r1')`)
	// Thread racine (folder forum + thread post) auquel rattacher les messages.
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,body_md,body_html,author_id) VALUES('thread1','le-thread','post','Le thread','corps','<p>corps</p>','u1')`)
	return db, "thread1"
}

func userCtx(id string) context.Context {
	return middleware.ContextWithUser(context.Background(), &identity.User{ID: id})
}

// TestForumPostCreate_RealInsert : create insère réellement un nœud type 'post'
// rattaché au thread, avec body_html non vide (rendu par le Store nodetree).
// Avant : INSERT ... kind/body (colonnes inexistantes) → échec.
func TestForumPostCreate_RealInsert(t *testing.T) {
	db, _ := newForumDB(t)
	act := actionByID(t, "forum.post.create")
	res, err := act.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"thread_slug":"le-thread","message":"Bonjour **monde**"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("create: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE type='post' AND parent_id='thread1' AND body_md='Bonjour **monde**'`); n != 1 {
		t.Fatalf("post non inséré sous le thread (n=%d) — FAUX OK", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE parent_id='thread1' AND body_html != '' AND author_id='u1'`); n != 1 {
		t.Fatalf("body_html vide ou author_id non posé — rendu Store absent")
	}
}

// TestForumReplyCreate_RealInsert : reply.create insère un post sous le parent_id fourni.
func TestForumReplyCreate_RealInsert(t *testing.T) {
	db, _ := newForumDB(t)
	act := actionByID(t, "forum.reply.create")
	res, err := act.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"parent_id":"thread1","message":"une reponse"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("reply: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE type='post' AND parent_id='thread1' AND body_md='une reponse'`); n != 1 {
		t.Fatalf("reply non insérée (n=%d)", n)
	}
}

// TestForumPostEditSelf_RealUpdate : edit_self change body_md (et re-rend body_html)
// du post de l'utilisateur courant. Avant : UPDATE nodes SET body=? (colonne inexistante).
func TestForumPostEditSelf_RealUpdate(t *testing.T) {
	db, _ := newForumDB(t)
	// Crée d'abord un post appartenant à u1.
	create := actionByID(t, "forum.post.create")
	res, err := create.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"thread_slug":"le-thread","message":"avant"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("create prep: %v %s", err, res.Status)
	}
	id := res.Data.(map[string]string)["node_id"]

	edit := actionByID(t, "forum.post.edit_self")
	res, err = edit.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"id":"`+id+`","message":"apres edition"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("edit: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='`+id+`' AND body_md='apres edition' AND body_html != ''`); n != 1 {
		t.Fatalf("body_md non modifié ou body_html vide — édition factice")
	}
}

// TestForumPostEditSelf_RejectsNonOwner : un autre utilisateur ne peut pas éditer.
func TestForumPostEditSelf_RejectsNonOwner(t *testing.T) {
	db, _ := newForumDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u2','u2@example.org','x','U2')`)
	create := actionByID(t, "forum.post.create")
	res, _ := create.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"thread_slug":"le-thread","message":"a u1"}`))
	id := res.Data.(map[string]string)["node_id"]

	edit := actionByID(t, "forum.post.edit_self")
	res, _ = edit.Run(userCtx("u2"), app.AppDeps{DB: db}, json.RawMessage(`{"id":"`+id+`","message":"piratage"}`))
	if res.Status != "error" {
		t.Fatalf("u2 a pu éditer le post de u1 — appartenance non vérifiée")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='`+id+`' AND body_md='a u1'`); n != 1 {
		t.Fatalf("post altéré malgré le refus")
	}
}

// TestForumPostDelete_RealDelete : delete (modérateur) fait disparaître le nœud
// de la base (hard DELETE, cohérent avec le guardian historique users_guardian_test).
func TestForumPostDelete_RealDelete(t *testing.T) {
	db, _ := newForumDB(t)
	create := actionByID(t, "forum.post.create")
	res, _ := create.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"thread_slug":"le-thread","message":"a supprimer"}`))
	id := res.Data.(map[string]string)["node_id"]

	del := actionByID(t, "forum.post.delete")
	res, err := del.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"id":"`+id+`","reason":"spam"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("delete: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='`+id+`'`); n != 0 {
		t.Fatalf("nœud non supprimé (n=%d) — FAUX OK", n)
	}
}

// TestForumPostDeleteSelf_RealDelete : idem pour delete_self.
func TestForumPostDeleteSelf_RealDelete(t *testing.T) {
	db, _ := newForumDB(t)
	create := actionByID(t, "forum.post.create")
	res, _ := create.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"thread_slug":"le-thread","message":"a supprimer self"}`))
	id := res.Data.(map[string]string)["node_id"]

	del := actionByID(t, "forum.post.delete_self")
	res, err := del.Run(userCtx("u1"), app.AppDeps{DB: db}, json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("delete_self: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='`+id+`'`); n != 0 {
		t.Fatalf("nœud non supprimé (n=%d)", n)
	}
}

// TestForumPostPinUnpin_RealUpdate : pin pose pinned=1, unpin remet 0.
// La colonne pinned n'existait pas avant 00022 → l'action échouait.
func TestForumPostPinUnpin_RealUpdate(t *testing.T) {
	db, _ := newForumDB(t)
	deps := app.AppDeps{DB: db}

	pin := actionByID(t, "forum.post.pin")
	res, err := pin.Run(userCtx("u1"), deps, json.RawMessage(`{"id":"thread1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("pin: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='thread1' AND pinned=1`); n != 1 {
		t.Fatalf("pinned non posé à 1")
	}

	unpin := actionByID(t, "forum.post.unpin")
	res, err = unpin.Run(userCtx("u1"), deps, json.RawMessage(`{"id":"thread1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("unpin: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='thread1' AND pinned=0`); n != 1 {
		t.Fatalf("pinned non remis à 0")
	}
}

// TestForumThreadLockUnlock_RealUpdate : lock pose locked=1, unlock remet 0.
func TestForumThreadLockUnlock_RealUpdate(t *testing.T) {
	db, _ := newForumDB(t)
	deps := app.AppDeps{DB: db}

	lock := actionByID(t, "forum.thread.lock")
	res, err := lock.Run(userCtx("u1"), deps, json.RawMessage(`{"id":"thread1","reason":"hors-sujet"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("lock: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='thread1' AND locked=1`); n != 1 {
		t.Fatalf("locked non posé à 1")
	}

	unlock := actionByID(t, "forum.thread.unlock")
	res, err = unlock.Run(userCtx("u1"), deps, json.RawMessage(`{"id":"thread1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("unlock: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='thread1' AND locked=0`); n != 1 {
		t.Fatalf("locked non remis à 0")
	}
}
