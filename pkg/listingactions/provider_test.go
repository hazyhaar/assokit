package listingactions_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/listingactions"
)

// testSilo est un silo minimal sans facette : suffit pour exercer create/update/
// set_status/open_inquiry/search sans dépendre du filesystem de config.
func testSilo() *listing.SiloDef {
	return &listing.SiloDef{ID: "test", Label: "Test"}
}

// newRegistry construit un registry peuplé par le provider sur un store silo.
func newRegistry(t *testing.T, store *listing.Store) *actions.Registry {
	t.Helper()
	reg := actions.NewRegistry()
	if err := listingactions.Register(store)(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return reg
}

// newStore ouvre un store silo en mémoire (DB dédiée annonces).
func newStore(t *testing.T) *listing.Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := listing.NewStore(context.Background(), db, testSilo())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// newCoreDB ouvre une DB core (schéma chassis) pour la messagerie d'open_inquiry.
func newCoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func find(t *testing.T, reg *actions.Registry, id string) actions.Action {
	t.Helper()
	a, ok := reg.Get(id)
	if !ok {
		t.Fatalf("action %q absente du registry", id)
	}
	return a
}

var allIDs = []string{
	"listing.create",
	"listing.update",
	"listing.set_status",
	"listing.open_inquiry",
	"listing.search",
	"listing.favorite",
	"listing.unfavorite",
	"listing.list_favorites",
	"listing.save_search",
	"listing.list_saved_searches",
}

// C1 : les 5 actions sont enregistrées avec ID/Title/Description/ParamsSchema/RequiredPerm.
func TestC1_ActionsRegisteredWithContract(t *testing.T) {
	reg := newRegistry(t, newStore(t))
	for _, id := range allIDs {
		a := find(t, reg, id)
		if a.Title == "" || a.Description == "" {
			t.Errorf("%s: Title/Description manquant", id)
		}
		if a.ParamsSchema == nil {
			t.Errorf("%s: ParamsSchema nil", id)
		}
		if a.RequiredPerm == "" {
			t.Errorf("%s: RequiredPerm vide", id)
		}
		if a.Run == nil {
			t.Errorf("%s: Run nil", id)
		}
	}
}

// C2 : PARITÉ. Les 5 actions sont présentes dans le registry monté — MountHTTP et
// MountMCP itèrent le MÊME reg.All(), donc présence ⇒ servies en HTTP /admin/actions
// ET en MCP par construction. On prouve qu'elles sortent toutes de reg.All().
func TestC2_ParityRegistryAll(t *testing.T) {
	reg := newRegistry(t, newStore(t))
	got := map[string]bool{}
	for _, a := range reg.All() {
		got[a.ID] = true
	}
	for _, id := range allIDs {
		if !got[id] {
			t.Errorf("%s absente de reg.All() — non servie HTTP/MCP", id)
		}
	}
}

// C3 : SÉCURITÉ (décisif). Un owner_id/buyer_id injecté dans les params est IGNORÉ ;
// l'identité effective vient de UserFromContext. Un user A ne peut pas créer au nom
// de B, ni éditer l'annonce de B.
func TestC3_InjectedIdentityParamIgnored(t *testing.T) {
	store := newStore(t)
	core := newCoreDB(t)
	for _, u := range []struct{ id, email string }{
		{"alice", "alice@example.org"},
		{"bob", "bob@example.org"},
		{"mallory", "mallory@example.org"},
	} {
		if _, err := core.Exec(`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES(?,?,?,?,1)`,
			u.id, u.email, "x", u.email); err != nil {
			t.Fatal(err)
		}
	}
	reg := newRegistry(t, store)
	deps := app.AppDeps{DB: core}
	ctxAlice := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})
	ctxBob := middleware.ContextWithUser(context.Background(), &identity.User{ID: "bob"})

	// create : alice tente d'usurper bob via owner_id en param → l'owner reste alice.
	create := find(t, reg, "listing.create")
	res, err := create.Run(ctxAlice, deps,
		json.RawMessage(`{"title":"Voilier","owner_id":"bob","price_cents":1000}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("create: status=%s err=%v", res.Status, err)
	}
	data := res.Data.(map[string]any)
	if data["owner_id"] != "alice" {
		t.Fatalf("owner_id usurpé : attendu alice, obtenu %v", data["owner_id"])
	}
	listingID := data["id"].(string)

	// update : bob (tiers) ne peut pas éditer l'annonce d'alice → ErrNotOwner.
	update := find(t, reg, "listing.update")
	res, err = update.Run(ctxBob, deps,
		json.RawMessage(`{"id":"`+listingID+`","title":"Piraté","owner_id":"bob"}`))
	if err != nil {
		t.Fatalf("update err inattendue: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("update par un tiers aurait dû échouer, status=%s", res.Status)
	}
	// L'annonce d'alice est intacte.
	got, err := store.Get(context.Background(), listingID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != "alice" || got.Title != "Voilier" {
		t.Fatalf("annonce altérée par un tiers: owner=%s title=%s", got.OwnerID, got.Title)
	}

	// open_inquiry : mallory s'enquiert ; buyer_id=alice en param est ignoré → buyer=mallory.
	ctxMallory := middleware.ContextWithUser(context.Background(), &identity.User{ID: "mallory"})
	inquiry := find(t, reg, "listing.open_inquiry")
	res, err = inquiry.Run(ctxMallory, deps,
		json.RawMessage(`{"listing_id":"`+listingID+`","buyer_id":"alice","body":"intéressé"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("open_inquiry: status=%s err=%v msg=%s", res.Status, err, res.Message)
	}
	idata := res.Data.(map[string]any)
	if idata["buyer_id"] != "mallory" {
		t.Fatalf("buyer_id usurpé : attendu mallory, obtenu %v", idata["buyer_id"])
	}
	if idata["owner_id"] != "alice" {
		t.Fatalf("owner_id de l'inquiry incorrect: %v", idata["owner_id"])
	}
}

// C4 : RBAC. Aucune action sans RequiredPerm ; Perms() expose les permissions à
// accorder à la bordure (cmd/yachts les octroie au grade membre).
func TestC4_RBACPerms(t *testing.T) {
	reg := newRegistry(t, newStore(t))
	declared := map[string]bool{}
	for _, p := range listingactions.Perms() {
		declared[p] = true
	}
	for _, id := range allIDs {
		a := find(t, reg, id)
		if a.RequiredPerm == "" {
			t.Errorf("%s: aucune perm requise", id)
		}
		if !declared[a.RequiredPerm] {
			t.Errorf("%s: perm %q non déclarée dans Perms()", id, a.RequiredPerm)
		}
	}
	if len(listingactions.Perms()) == 0 {
		t.Fatal("Perms() vide")
	}
}

// C5 : un store nil est une erreur de câblage fail-loud.
func TestC5_RegisterNilStore(t *testing.T) {
	if err := listingactions.Register(nil)(actions.NewRegistry()); err == nil {
		t.Fatal("Register(nil) aurait dû échouer")
	}
}
