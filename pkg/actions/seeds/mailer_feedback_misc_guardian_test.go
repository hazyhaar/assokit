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

// Gardiens comportementaux S5 — domaines mailer.outbox, feedback, plus actions
// isolées (forum.thread.move, users.list, profile.edit_self, messages.*).
// Chaque test prouve l'effet/lecture RÉEL en base, jamais un faux OK. Quand
// l'action vise une table/colonne inexistante (stub), l'assertion porte sur
// l'effet attendu correct : le rouge révèle le stub (rapporté, non adouci).

func guardMFDB(t *testing.T) *sql.DB {
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

// ---------------------------------------------------------------------------
// mailer.outbox.* — la table réelle est email_outbox (statut pending/sent/failed).
// Les actions ciblent mailer_outbox (inexistante) → stub révélé.
// ---------------------------------------------------------------------------

// TestGuardMailer_List : list doit lire les emails de l'outbox réelle.
func TestGuardMailer_List(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO email_outbox(id,to_addr,subject,status)
		VALUES('e1','dest@example.org','Bonjour','pending')`)

	act := actionByID(t, "mailer.outbox.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("list: status=%s err=%v (stub probable : table mailer_outbox vs email_outbox)", res.Status, err)
	}
	rows, ok := res.Data.([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("list: attendu 1 ligne lue depuis email_outbox, data=%#v", res.Data)
	}
}

// TestGuardMailer_Cancel : cancel doit passer le statut d'un email pending à cancelled.
func TestGuardMailer_Cancel(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO email_outbox(id,to_addr,subject,status)
		VALUES('e1','dest@example.org','Bonjour','pending')`)

	act := actionByID(t, "mailer.outbox.cancel")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"id":"e1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("cancel: status=%s err=%v (stub : table mailer_outbox vs email_outbox)", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM email_outbox WHERE id='e1' AND status='cancelled'`); n != 1 {
		t.Fatalf("cancel sans effet réel : email non passé à 'cancelled' (n=%d) — FAUX OK / stub", n)
	}
}

// TestGuardMailer_Retry : retry doit replanifier un email (statut → pending).
func TestGuardMailer_Retry(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO email_outbox(id,to_addr,subject,status)
		VALUES('e1','dest@example.org','Bonjour','failed')`)

	act := actionByID(t, "mailer.outbox.retry")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"id":"e1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("retry: status=%s err=%v (stub : table mailer_outbox vs email_outbox)", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM email_outbox WHERE id='e1' AND status='pending'`); n != 1 {
		t.Fatalf("retry sans effet réel : email non repassé à 'pending' (n=%d) — FAUX OK / stub", n)
	}
}

// ---------------------------------------------------------------------------
// feedback.* — table réelle feedbacks (status pending/triaged/closed/spam,
// colonne admin_note ; message 5..2000). triage cible triage_note + status
// processed/archived → stub révélé.
// ---------------------------------------------------------------------------

// TestGuardFeedback_Create : create doit insérer une ligne pending.
func TestGuardFeedback_Create(t *testing.T) {
	db := guardMFDB(t)
	act := actionByID(t, "feedback.create")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db},
		json.RawMessage(`{"page_url":"/accueil","message":"Très bon site, bravo"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("create: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM feedbacks WHERE page_url='/accueil' AND status='pending'`); n != 1 {
		t.Fatalf("create sans effet réel : feedback non inséré (n=%d) — FAUX OK", n)
	}
}

// TestGuardFeedback_List : list doit lire les feedbacks insérés.
func TestGuardFeedback_List(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO feedbacks(id,page_url,message,status)
		VALUES('f1','/accueil','Un message de test','pending')`)

	act := actionByID(t, "feedback.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("list: status=%s err=%v", res.Status, err)
	}
	rows, ok := res.Data.([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("list: attendu 1 feedback lu, data=%#v", res.Data)
	}
}

// TestGuardFeedback_Triage : triage doit changer le statut d'un feedback seedé.
// L'action écrit status='processed' (hors CHECK) et colonne triage_note
// (inexistante) → on assert l'effet métier correct : statut 'triaged'.
func TestGuardFeedback_Triage(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO feedbacks(id,page_url,message,status)
		VALUES('f1','/accueil','Un message de test','pending')`)

	act := actionByID(t, "feedback.triage")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db},
		json.RawMessage(`{"id":"f1","status":"processed","note":"vu"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("triage: status=%s err=%v (stub : colonne triage_note inexistante / status hors CHECK)", res.Status, err)
	}
	// Effet réel attendu : le feedback n'est plus 'pending'.
	if n := count(t, db, `SELECT COUNT(*) FROM feedbacks WHERE id='f1' AND status='pending'`); n != 0 {
		t.Fatalf("triage sans effet réel : feedback toujours 'pending' — FAUX OK / stub")
	}
}

// ---------------------------------------------------------------------------
// forum.thread.move — re-parentage d'un nœud (change parent_id).
// ---------------------------------------------------------------------------

// TestGuardForum_ThreadMove : déplace un thread vers une autre catégorie.
func TestGuardForum_ThreadMove(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title) VALUES('cat1','cat-1','folder','Cat 1')`)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title) VALUES('cat2','cat-2','folder','Cat 2')`)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,parent_id) VALUES('thr','thread-1','post','Thread','cat1')`)

	act := actionByID(t, "forum.thread.move")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db},
		json.RawMessage(`{"id":"thr","dest_parent":"cat2"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("move: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='thr' AND parent_id='cat2'`); n != 1 {
		t.Fatalf("move sans effet réel : thread non re-parenté vers cat2 — FAUX OK")
	}
}

// ---------------------------------------------------------------------------
// users.list — lecture.
// ---------------------------------------------------------------------------

// TestGuardUsers_List : list doit retourner les utilisateurs existants.
func TestGuardUsers_List(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u2','u2@example.org','x','U2')`)

	act := actionByID(t, "users.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("users.list: status=%s err=%v", res.Status, err)
	}
	rows, ok := res.Data.([]map[string]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("users.list: attendu 2 utilisateurs lus, data=%#v", res.Data)
	}
}

// ---------------------------------------------------------------------------
// profile.edit_self — met à jour le profil de l'utilisateur courant.
// L'action écrit bio/website (colonnes inexistantes) → stub révélé. On vérifie
// l'effet métier réel : display_name de l'utilisateur courant mis à jour.
// ---------------------------------------------------------------------------

// TestGuardProfile_EditSelf : modifie le profil de l'utilisateur en contexte.
func TestGuardProfile_EditSelf(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','Ancien')`)

	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "u1"})
	act := actionByID(t, "profile.edit_self")
	res, err := act.Run(ctx, app.AppDeps{DB: db},
		json.RawMessage(`{"display_name":"Nouveau"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("edit_self: status=%s err=%v (stub : colonnes bio/website inexistantes)", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND display_name='Nouveau'`); n != 1 {
		t.Fatalf("edit_self sans effet réel : display_name non mis à jour pour l'utilisateur courant — FAUX OK / stub")
	}
}

// ---------------------------------------------------------------------------
// messages.inbox — lecture des conversations de l'utilisateur courant.
// messages.mark_read — pose read_at sur les messages reçus non lus.
// ---------------------------------------------------------------------------

// TestGuardMessages_Inbox : inbox retourne une conversation avec non-lu.
func TestGuardMessages_Inbox(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('alice','alice@example.org','x','Alice')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('bob','bob@example.org','x','Bob')`)
	mustExec(t, db, `INSERT INTO messages(id,sender_id,recipient_id,body_md) VALUES('m1','bob','alice','coucou alice')`)

	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})
	act := actionByID(t, "messages.inbox")
	res, err := act.Run(ctx, app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("inbox: status=%s err=%v", res.Status, err)
	}
	rows, ok := res.Data.([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("inbox: attendu 1 conversation, data=%#v", res.Data)
	}
	if rows[0]["with_user_id"] != "bob" {
		t.Fatalf("inbox: correspondant attendu bob, got %v", rows[0]["with_user_id"])
	}
}

// TestGuardMessages_MarkRead : pose read_at sur les messages reçus non lus.
func TestGuardMessages_MarkRead(t *testing.T) {
	db := guardMFDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('alice','alice@example.org','x','Alice')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('bob','bob@example.org','x','Bob')`)
	mustExec(t, db, `INSERT INTO messages(id,sender_id,recipient_id,body_md) VALUES('m1','bob','alice','non lu')`)

	if n := count(t, db, `SELECT COUNT(*) FROM messages WHERE id='m1' AND read_at IS NULL`); n != 1 {
		t.Fatalf("précondition : message m1 devrait être non lu")
	}

	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})
	act := actionByID(t, "messages.mark_read")
	res, err := act.Run(ctx, app.AppDeps{DB: db}, json.RawMessage(`{"with_user_id":"bob"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("mark_read: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM messages WHERE id='m1' AND read_at IS NOT NULL`); n != 1 {
		t.Fatalf("mark_read sans effet réel : read_at non posé — FAUX OK")
	}
}
