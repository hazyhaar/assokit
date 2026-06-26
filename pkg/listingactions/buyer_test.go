package listingactions_test

import (
	"context"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/listing"
)

// seedListing crée une annonce et retourne son id, via le store directement.
func seedListing(t *testing.T, store *listing.Store, owner, title string) string {
	t.Helper()
	l := &listing.Listing{OwnerID: owner, Title: title, Status: listing.StatusPublished}
	if err := store.Create(context.Background(), l); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	return l.ID
}

// C4 acheteur : l'identité des actions favoris/saved-searches vient TOUJOURS du
// contexte ; un user_id injecté dans les params est ignoré. Alice favorise une
// annonce en passant user_id=bob → c'est bien alice qui possède le favori.
func TestBuyer_InjectedUserIDIgnored(t *testing.T) {
	store := newStore(t)
	reg := newRegistry(t, store)
	deps := app.AppDeps{}
	lid := seedListing(t, store, "broker", "NEEL 43")

	ctxAlice := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})
	ctxBob := middleware.ContextWithUser(context.Background(), &identity.User{ID: "bob"})

	fav := find(t, reg, "listing.favorite")
	res, err := fav.Run(ctxAlice, deps,
		json.RawMessage(`{"listing_id":"`+lid+`","user_id":"bob"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("favorite: status=%s err=%v msg=%s", res.Status, err, res.Message)
	}
	if got := res.Data.(map[string]any)["user_id"]; got != "alice" {
		t.Fatalf("user_id usurpé : attendu alice, obtenu %v", got)
	}

	// alice voit son favori ; bob ne voit rien (cloisonnement par identité-contexte).
	listFav := find(t, reg, "listing.list_favorites")
	resA, _ := listFav.Run(ctxAlice, deps, json.RawMessage(`{}`))
	if n := resA.Data.(map[string]any)["count"].(int); n != 1 {
		t.Fatalf("alice devrait voir 1 favori, vu %d", n)
	}
	resB, _ := listFav.Run(ctxBob, deps, json.RawMessage(`{}`))
	if n := resB.Data.(map[string]any)["count"].(int); n != 0 {
		t.Fatalf("bob ne devrait voir aucun favori (le param user_id=bob a été ignoré), vu %d", n)
	}

	// save_search : alice sauve, user_id=bob ignoré → propriétaire alice.
	save := find(t, reg, "listing.save_search")
	resS, err := save.Run(ctxAlice, deps,
		json.RawMessage(`{"name":"trimarans","text":"NEEL","user_id":"bob"}`))
	if err != nil || resS.Status != "ok" {
		t.Fatalf("save_search: status=%s err=%v", resS.Status, err)
	}
	if got := resS.Data.(map[string]any)["user_id"]; got != "alice" {
		t.Fatalf("save_search user_id usurpé : %v", got)
	}
	listSS := find(t, reg, "listing.list_saved_searches")
	resLA, _ := listSS.Run(ctxAlice, deps, json.RawMessage(`{}`))
	if n := resLA.Data.(map[string]any)["count"].(int); n != 1 {
		t.Fatalf("alice devrait voir 1 recherche, vu %d", n)
	}
	resLB, _ := listSS.Run(ctxBob, deps, json.RawMessage(`{}`))
	if n := resLB.Data.(map[string]any)["count"].(int); n != 0 {
		t.Fatalf("bob ne devrait voir aucune recherche, vu %d", n)
	}
}

// Les actions acheteur refusent un contexte sans utilisateur (fail-loud).
func TestBuyer_RequiresAuth(t *testing.T) {
	reg := newRegistry(t, newStore(t))
	deps := app.AppDeps{}
	for _, id := range []string{
		"listing.favorite", "listing.unfavorite", "listing.list_favorites",
		"listing.save_search", "listing.list_saved_searches",
	} {
		a := find(t, reg, id)
		res, err := a.Run(context.Background(), deps, json.RawMessage(`{"listing_id":"x","name":"x"}`))
		if err != nil {
			t.Fatalf("%s: err inattendue %v", id, err)
		}
		if res.Status != "error" {
			t.Fatalf("%s sans auth aurait dû échouer, status=%s", id, res.Status)
		}
	}
}
