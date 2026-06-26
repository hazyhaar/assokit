package seeds_test

import (
	"context"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// Ce fichier contient les tests gardiens comportementaux (strate S5) des actions
// registry des domaines events, memberships et newsletter. But : prouver que
// chaque action MUTE/LIT réellement la DB, et non qu'elle renvoie un faux OK.
//
// Helpers réutilisés depuis account_test.go (même package seeds_test) :
//   actionByID(t, id), mustExec(t, db, q), count(t, db, q).

// guardDB (DB mémoire + FK + schéma) est défini dans
// pages_signup_branding_setup_guardian_test.go (même package seeds_test) et
// réutilisé ici.

// ----------------------------------------------------------------------------
// events
// ----------------------------------------------------------------------------

// TestGuardEvents_Create : events.create doit INSÉRER une ligne réelle dans
// events (slug auto, created_by = utilisateur courant).
func TestGuardEvents_Create(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	deps := app.AppDeps{DB: db}
	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "u1"})

	act := actionByID(t, "events.create")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"title":"Assemblee Generale 2026","starts_at":"2026-06-15T18:30"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("events.create: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM events WHERE title='Assemblee Generale 2026' AND created_by='u1' AND deleted_at IS NULL`); n != 1 {
		t.Fatalf("événement non inséré (n=%d) — FAUX OK détecté", n)
	}
	// Slug auto-généré depuis le titre (slugify : minuscules, séparateurs '-').
	if n := count(t, db, `SELECT COUNT(*) FROM events WHERE slug='assemblee-generale-2026'`); n != 1 {
		t.Fatalf("slug auto non généré")
	}
}

// TestGuardEvents_List : events.list doit renvoyer ok ET refléter les lignes
// seedées (événements non supprimés uniquement).
func TestGuardEvents_List(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO events(id,slug,title,starts_at) VALUES('e1','s1','Vif','2026-07-01T10:00')`)
	mustExec(t, db, `INSERT INTO events(id,slug,title,starts_at) VALUES('e2','s2','Passé','2025-01-01T10:00')`)
	mustExec(t, db, `INSERT INTO events(id,slug,title,starts_at,deleted_at) VALUES('e3','s3','Effacé','2026-08-01T10:00','2026-05-01 00:00:00')`)

	act := actionByID(t, "events.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("events.list: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("events.list: Data type %T inattendu", res.Data)
	}
	if len(list) != 2 {
		t.Fatalf("events.list: %d éléments, attendu 2 (l'effacé exclu)", len(list))
	}
	got := map[string]bool{}
	for _, m := range list {
		got[m["title"].(string)] = true
	}
	if !got["Vif"] || !got["Passé"] || got["Effacé"] {
		t.Fatalf("events.list ne reflète pas les lignes seedées : %v", got)
	}
}

// TestGuardEvents_Get : events.get doit renvoyer ok ET la ligne ciblée (par slug).
func TestGuardEvents_Get(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO events(id,slug,title,starts_at,location) VALUES('e1','reunion','Réunion','2026-09-01T19:00','Salle B')`)

	act := actionByID(t, "events.get")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"id_or_slug":"reunion"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("events.get: status=%s err=%v", res.Status, err)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("events.get: Data type %T inattendu", res.Data)
	}
	if m["id"] != "e1" || m["title"] != "Réunion" || m["location"] != "Salle B" {
		t.Fatalf("events.get ne reflète pas la ligne seedée : %v", m)
	}
}

// TestGuardEvents_Delete : events.delete doit MARQUER deleted_at (soft-delete réel).
func TestGuardEvents_Delete(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO events(id,slug,title,starts_at) VALUES('e1','s1','À supprimer','2026-10-01T10:00')`)

	act := actionByID(t, "events.delete")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"id":"e1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("events.delete: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM events WHERE id='e1' AND deleted_at IS NOT NULL`); n != 1 {
		t.Fatalf("événement non soft-deleté (n=%d) — FAUX OK détecté", n)
	}
}

// ----------------------------------------------------------------------------
// memberships
// ----------------------------------------------------------------------------

// TestGuardMemberships_Record : memberships.record doit INSÉRER une adhésion
// réelle pour un membre existant et actif.
func TestGuardMemberships_Record(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)

	act := actionByID(t, "memberships.record")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(
		`{"user_id":"u1","period_start":"2026-01-01","period_end":"2026-12-31","amount_cents":2500,"status":"active","note":"chèque"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("memberships.record: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM memberships WHERE user_id='u1' AND amount_cents=2500 AND status='active' AND note='chèque'`); n != 1 {
		t.Fatalf("adhésion non insérée (n=%d) — FAUX OK détecté", n)
	}
}

// TestGuardMemberships_List : memberships.list doit renvoyer ok ET refléter les
// lignes seedées, avec filtre par statut effectif.
func TestGuardMemberships_List(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u2','u2@example.org','x','U2')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES('m1','u1','2026-01-01','2026-12-31','active')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES('m2','u2','2026-01-01','2026-12-31','pending')`)

	act := actionByID(t, "memberships.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("memberships.list: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("memberships.list: Data type %T inattendu", res.Data)
	}
	if len(list) != 2 {
		t.Fatalf("memberships.list: %d éléments, attendu 2", len(list))
	}

	// Filtre par statut : doit ne renvoyer que les actives.
	res2, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"status":"active"}`))
	if err != nil || res2.Status != "ok" {
		t.Fatalf("memberships.list status=active: status=%s err=%v", res2.Status, err)
	}
	list2 := res2.Data.([]map[string]any)
	if len(list2) != 1 || list2[0]["id"] != "m1" {
		t.Fatalf("filtre statut non appliqué : %v", list2)
	}
}

// TestGuardMemberships_Mine : memberships.mine doit renvoyer ok ET uniquement les
// adhésions de l'utilisateur courant.
func TestGuardMemberships_Mine(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u2','u2@example.org','x','U2')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES('m1','u1','2026-01-01','2026-12-31','active')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES('m2','u2','2026-01-01','2026-12-31','active')`)

	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "u1"})
	act := actionByID(t, "memberships.mine")
	res, err := act.Run(ctx, app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("memberships.mine: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("memberships.mine: Data type %T inattendu", res.Data)
	}
	if len(list) != 1 || list[0]["id"] != "m1" || list[0]["user_id"] != "u1" {
		t.Fatalf("memberships.mine ne reflète pas les adhésions de u1 : %v", list)
	}
}

// TestGuardMemberships_SetStatus : memberships.set_status doit MUTER le statut en DB.
func TestGuardMemberships_SetStatus(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end,status) VALUES('m1','u1','2026-01-01','2026-12-31','pending')`)

	act := actionByID(t, "memberships.set_status")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"id":"m1","status":"active"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("memberships.set_status: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM memberships WHERE id='m1' AND status='active'`); n != 1 {
		t.Fatalf("statut non muté (n=%d) — FAUX OK détecté", n)
	}
}

// ----------------------------------------------------------------------------
// newsletter
// ----------------------------------------------------------------------------

// fakeMailer compte les appels Enqueue (mailer factice, aucun envoi réel).
type fakeMailer struct {
	calls []string // adresses enfilées
}

func (m *fakeMailer) Enqueue(_ context.Context, to, _ string, _ string, _ string) error {
	m.calls = append(m.calls, to)
	return nil
}

// TestGuardNewsletter_Create : newsletter.create doit INSÉRER une diffusion réelle
// (brouillon, sent_at NULL, created_by = admin courant).
func TestGuardNewsletter_Create(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('admin','a@example.org','x','Admin')`)

	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "admin"})
	act := actionByID(t, "newsletter.create")
	res, err := act.Run(ctx, app.AppDeps{DB: db}, json.RawMessage(`{"subject":"Hello","body":"# Salut"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("newsletter.create: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM newsletters WHERE subject='Hello' AND created_by='admin' AND sent_at IS NULL`); n != 1 {
		t.Fatalf("diffusion non insérée (n=%d) — FAUX OK détecté", n)
	}
}

// TestGuardNewsletter_List : newsletter.list doit renvoyer ok ET refléter les
// diffusions seedées.
func TestGuardNewsletter_List(t *testing.T) {
	db := guardDB(t)
	mustExec(t, db, `INSERT INTO newsletters(id,subject,body_md) VALUES('n1','Sujet A','corps')`)
	mustExec(t, db, `INSERT INTO newsletters(id,subject,body_md,sent_at,recipients_count) VALUES('n2','Sujet B','corps','2026-05-01 00:00:00',3)`)

	act := actionByID(t, "newsletter.list")
	res, err := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("newsletter.list: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok {
		t.Fatalf("newsletter.list: Data type %T inattendu", res.Data)
	}
	if len(list) != 2 {
		t.Fatalf("newsletter.list: %d éléments, attendu 2", len(list))
	}
	subjects := map[string]bool{}
	for _, m := range list {
		subjects[m["subject"].(string)] = true
	}
	if !subjects["Sujet A"] || !subjects["Sujet B"] {
		t.Fatalf("newsletter.list ne reflète pas les diffusions seedées : %v", subjects)
	}
}

// TestGuardNewsletter_Send : newsletter.send doit appeler Enqueue pour chaque
// membre actif (is_active=1) ET marquer sent_at + recipients_count en DB.
func TestGuardNewsletter_Send(t *testing.T) {
	db := guardDB(t)
	// Deux membres actifs, un inactif (ne doit pas recevoir).
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u1','u1@example.org','x','U1',1)`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u2','u2@example.org','x','U2',1)`)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u3','u3@example.org','x','U3',0)`)
	mustExec(t, db, `INSERT INTO newsletters(id,subject,body_md) VALUES('n1','Sujet','corps')`)

	mailer := &fakeMailer{}
	deps := app.AppDeps{DB: db, Mailer: mailer}

	act := actionByID(t, "newsletter.send")
	res, err := act.Run(context.Background(), deps, json.RawMessage(`{"id":"n1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("newsletter.send: status=%s err=%v", res.Status, err)
	}
	// Enqueue appelé pour les 2 membres actifs uniquement.
	if len(mailer.calls) != 2 {
		t.Fatalf("Enqueue appelé %d fois, attendu 2 (membres actifs) : %v", len(mailer.calls), mailer.calls)
	}
	// sent_at posé + recipients_count = 2 en DB.
	if n := count(t, db, `SELECT COUNT(*) FROM newsletters WHERE id='n1' AND sent_at IS NOT NULL AND recipients_count=2`); n != 1 {
		t.Fatalf("diffusion non marquée envoyée (n=%d) — FAUX OK détecté", n)
	}
}
