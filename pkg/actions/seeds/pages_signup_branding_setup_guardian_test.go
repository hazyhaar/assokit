package seeds_test

// Tests gardiens comportementaux — domaines pages, signup, branding, setup (strate S5).
// But : prouver que chaque action MUTE ou LIT réellement la base, et non qu'elle
// répond "ok" en contournant un schéma absent (faux OK historique).
//
// Helpers réutilisés depuis account_test.go (même package seeds_test) :
//   actionByID(t, id), mustExec(t, db, q), count(t, db, q).
//
// Convention obligatoire : act := actionByID(t, "ID_EXACT") ;
//                          act.Run(ctx, deps, json.RawMessage(...)).

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
)

// guardDB ouvre une base mémoire migrée (schéma réel complet), FK actives.
func guardDB(t *testing.T) *sql.DB {
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

// -----------------------------------------------------------------------------
// pages
// -----------------------------------------------------------------------------

// TestGuardPages_Create : la page doit réellement exister en base après create.
// Effet réel attendu : une ligne nodes de type 'page' avec le slug fourni.
func TestGuardPages_Create(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()

	act := actionByID(t, "pages.create")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"slug":"reglement","body_md":"# Reglement"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("pages.create: status=%s err=%v", res.Status, err)
	}
	// Effet réel : la page doit être lisible dans nodes (type='page').
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE slug='reglement' AND type='page'`); n != 1 {
		t.Fatalf("page non créée en base (n=%d) — FAUX OK détecté", n)
	}
}

// TestGuardPages_Update : le corps de la page doit réellement changer en base.
func TestGuardPages_Update(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	// Seed direct au schéma réel (colonnes type, title, body_md obligatoires).
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,body_md) VALUES('p1','reglement','page','Reglement','ancien')`)

	act := actionByID(t, "pages.update")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"slug":"reglement","body_md":"nouveau"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("pages.update: status=%s err=%v", res.Status, err)
	}
	// Effet réel : le corps doit être 'nouveau'.
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE slug='reglement' AND body_md='nouveau'`); n != 1 {
		t.Fatalf("page non mise à jour (body_md inchangé) — FAUX OK détecté")
	}
}

// TestGuardPages_Delete : la page doit réellement disparaître de la base.
func TestGuardPages_Delete(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,body_md) VALUES('p1','reglement','page','Reglement','x')`)

	act := actionByID(t, "pages.delete")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"slug":"reglement"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("pages.delete: status=%s err=%v", res.Status, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE slug='reglement'`); n != 0 {
		t.Fatalf("page non supprimée (n=%d) — FAUX OK détecté", n)
	}
}

// TestGuardPages_List : doit lire et renvoyer les pages existantes.
func TestGuardPages_List(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,body_md) VALUES('p1','accueil','page','Accueil','x')`)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,body_md) VALUES('p2','contact','page','Contact','y')`)

	act := actionByID(t, "pages.list")
	res, err := act.Run(ctx, deps, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("pages.list: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("pages.list n'a pas renvoyé les 2 pages réelles (data=%#v) — lecture cassée", res.Data)
	}
}

// -----------------------------------------------------------------------------
// signup
// -----------------------------------------------------------------------------

// TestGuardSignup_Activate : l'activation doit réellement basculer l'état du
// compte (effet observable en base : compte actif).
func TestGuardSignup_Activate(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	// Compte inactif au schéma réel (users.is_active, pas users.status).
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u1','u1@example.org','x','U1',0)`)

	act := actionByID(t, "signup.activate")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"id":"u1"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("signup.activate: status=%s err=%v", res.Status, err)
	}
	// Effet réel attendu : le compte est désormais actif.
	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND is_active=1`); n != 1 {
		t.Fatalf("compte non activé (is_active toujours 0) — FAUX OK détecté")
	}
}

// TestGuardSignup_Reject : le rejet doit réellement marquer le compte rejeté
// (effet observable : compte désactivé).
func TestGuardSignup_Reject(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES('u1','u1@example.org','x','U1',1)`)

	act := actionByID(t, "signup.reject")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"id":"u1","reason":"hors charte"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("signup.reject: status=%s err=%v", res.Status, err)
	}
	// Effet réel attendu : le compte n'est plus actif (rejeté).
	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND is_active=0`); n != 1 {
		t.Fatalf("compte non rejeté (is_active toujours 1) — FAUX OK détecté")
	}
}

// TestGuardSignup_List : doit lire la liste des inscriptions réelles.
func TestGuardSignup_List(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	// Inscriptions au schéma réel (table signups).
	mustExec(t, db, `INSERT INTO signups(id,email,display_name,profile) VALUES('s1','a@example.org','A','membre')`)
	mustExec(t, db, `INSERT INTO signups(id,email,display_name,profile) VALUES('s2','b@example.org','B','membre')`)

	act := actionByID(t, "signup.list")
	res, err := act.Run(ctx, deps, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("signup.list: status=%s err=%v", res.Status, err)
	}
	list, ok := res.Data.([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("signup.list n'a pas renvoyé les 2 inscriptions réelles (data=%#v) — lecture cassée", res.Data)
	}
}

// -----------------------------------------------------------------------------
// branding
// -----------------------------------------------------------------------------

// TestGuardBranding_Set : set doit réellement persister la valeur (branding_kv).
func TestGuardBranding_Set(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()

	act := actionByID(t, "branding.set")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"field":"site_name","value":"Mon Asso"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("branding.set: status=%s err=%v", res.Status, err)
	}
	// Effet réel : la valeur doit être lisible dans branding_kv (key/value).
	var v string
	if err := db.QueryRow(`SELECT value FROM branding_kv WHERE key='site_name'`).Scan(&v); err != nil {
		t.Fatalf("valeur branding non persistée dans branding_kv: %v — FAUX OK détecté", err)
	}
	if v != "Mon Asso" {
		t.Fatalf("valeur branding incorrecte: %q", v)
	}
}

// TestGuardBranding_Get : get doit lire la valeur réellement présente en base.
func TestGuardBranding_Get(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()
	// Valeur posée au schéma réel (branding_kv key/value).
	mustExec(t, db, `INSERT INTO branding_kv(key,value) VALUES('site_name','Mon Asso')`)

	act := actionByID(t, "branding.get")
	res, err := act.Run(ctx, deps, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("branding.get: status=%s err=%v", res.Status, err)
	}
	m, ok := res.Data.(map[string]string)
	if !ok {
		t.Fatalf("branding.get data type inattendu: %#v", res.Data)
	}
	if m["site_name"] != "Mon Asso" {
		t.Fatalf("branding.get n'a pas lu la valeur réelle (site_name=%q) — lecture cassée", m["site_name"])
	}
}

// -----------------------------------------------------------------------------
// setup
// -----------------------------------------------------------------------------

// TestGuardSetup_Status : le diagnostic doit renvoyer ok + une liste d'items.
func TestGuardSetup_Status(t *testing.T) {
	db := guardDB(t)
	deps := app.AppDeps{DB: db}
	ctx := context.Background()

	act := actionByID(t, "setup.status")
	res, err := act.Run(ctx, deps, json.RawMessage(`{}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("setup.status: status=%s err=%v", res.Status, err)
	}
	items, ok := res.Data.([]map[string]any)
	if !ok || len(items) == 0 {
		t.Fatalf("setup.status n'a pas renvoyé d'items de diagnostic (data=%#v)", res.Data)
	}
	// Chaque item doit porter au moins une clé et un état (diagnostic cohérent).
	for _, it := range items {
		if it["key"] == "" || it["state"] == "" {
			t.Fatalf("item de diagnostic incomplet: %#v", it)
		}
	}
}
