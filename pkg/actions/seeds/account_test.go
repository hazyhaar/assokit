package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func actionByID(t *testing.T, id string) actions.Action {
	t.Helper()
	reg := actions.NewRegistry()
	seeds.InitAll(reg)
	for _, a := range reg.All() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("action %q absente", id)
	return actions.Action{}
}

// TestAccountDeleteSelf_ReallyDeletes : guardian comportemental — l'action doit
// EFFACER l'utilisateur (et cascader/anonymiser ses données), pas seulement
// répondre "ok". Couvre le faux-OK historique (M-ASSOKIT-AUDIT / S5).
func TestAccountDeleteSelf_ReallyDeletes(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	// Utilisateur + données liées : un nœud authored (anonymisation attendue) et
	// une adhésion (cascade attendue).
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES('u1','u1@example.org','x','U1')`)
	mustExec(t, db, `INSERT INTO nodes(id,slug,type,title,author_id) VALUES('n1','s1','post','T','u1')`)
	mustExec(t, db, `INSERT INTO memberships(id,user_id,period_start,period_end) VALUES('m1','u1','2026-01-01','2026-12-31')`)

	deps := app.AppDeps{DB: db}
	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "u1"})

	act := actionByID(t, "account.delete_self")
	res, err := act.Run(ctx, deps, json.RawMessage(`{"confirm":"DELETE_MY_ACCOUNT"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("delete_self: status=%s err=%v", res.Status, err)
	}

	// L'utilisateur n'existe plus.
	if n := count(t, db, `SELECT COUNT(*) FROM users WHERE id='u1'`); n != 0 {
		t.Fatalf("utilisateur non supprimé (n=%d) — FAUX OK détecté", n)
	}
	// L'adhésion a cascadé.
	if n := count(t, db, `SELECT COUNT(*) FROM memberships WHERE user_id='u1'`); n != 0 {
		t.Fatalf("adhésion non cascadée (n=%d)", n)
	}
	// Le nœud est conservé mais anonymisé (author_id NULL).
	if n := count(t, db, `SELECT COUNT(*) FROM nodes WHERE id='n1' AND author_id IS NULL`); n != 1 {
		t.Fatalf("nœud non anonymisé")
	}
}

// TestAccountDeleteSelf_RequiresAuth : sans utilisateur en contexte, refus.
func TestAccountDeleteSelf_RequiresAuth(t *testing.T) {
	db, _ := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	defer db.Close()
	db.SetMaxOpenConns(1)
	_ = chassis.Run(db)
	act := actionByID(t, "account.delete_self")
	res, _ := act.Run(context.Background(), app.AppDeps{DB: db}, json.RawMessage(`{"confirm":"DELETE_MY_ACCOUNT"}`))
	if res.Status != "error" {
		t.Fatalf("sans auth : status=%s, attendu error", res.Status)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func count(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
