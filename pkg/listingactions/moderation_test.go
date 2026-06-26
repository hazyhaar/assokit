package listingactions_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/listingactions"
)

// C1 : listing.report enregistre un signalement dont le reporter est l'identité
// du CONTEXTE — un reporter_id en param ne peut pas usurper l'identité.
func TestReportActionUsesContextIdentity(t *testing.T) {
	store := newStore(t)
	reg := newRegistry(t, store)
	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "alice"})

	l := &listing.Listing{OwnerID: "seller", Title: "Voilier"}
	if err := store.Create(context.Background(), l); err != nil {
		t.Fatal(err)
	}

	report := find(t, reg, "listing.report")
	res, err := report.Run(ctx, app.AppDeps{},
		json.RawMessage(`{"listing_id":"`+l.ID+`","reason":"arnaque","reporter_id":"mallory"}`))
	if err != nil || res.Status != "ok" {
		t.Fatalf("report: status=%s err=%v msg=%s", res.Status, err, res.Message)
	}
	data := res.Data.(map[string]any)
	if data["reporter_id"] != "alice" {
		t.Fatalf("reporter_id usurpé via param: attendu alice, obtenu %v", data["reporter_id"])
	}
}

// C1bis : sans utilisateur en contexte, report est refusé (jamais d'identité
// dérivée d'un paramètre).
func TestReportActionRejectsAnonymous(t *testing.T) {
	reg := newRegistry(t, newStore(t))
	report := find(t, reg, "listing.report")
	res, _ := report.Run(context.Background(), app.AppDeps{},
		json.RawMessage(`{"listing_id":"x","reason":"y"}`))
	if res.Status != "error" {
		t.Fatalf("report anonyme aurait dû échouer, status=%s", res.Status)
	}
}

// C4 : la perm de modération est ÉLEVÉE et SÉPARÉE des perms membre. report est
// membre (dans Perms()) ; moderate_hide/restore exigent PermModerate, qui n'est
// PAS dans Perms() (membre) mais dans ModeratorPerms().
func TestModeratePermSeparatedFromMember(t *testing.T) {
	reg := newRegistry(t, newStore(t))

	memberPerms := listingactions.Perms()
	modPerms := listingactions.ModeratorPerms()

	if !slices.Contains(memberPerms, listingactions.PermReport) {
		t.Errorf("report devrait être une perm membre (Perms())")
	}
	if slices.Contains(memberPerms, listingactions.PermModerate) {
		t.Errorf("PermModerate ne doit PAS être une perm membre")
	}
	if !slices.Contains(modPerms, listingactions.PermModerate) {
		t.Errorf("PermModerate devrait être dans ModeratorPerms()")
	}

	for _, id := range []string{"listing.moderate_hide", "listing.moderate_restore"} {
		a := find(t, reg, id)
		if a.RequiredPerm != listingactions.PermModerate {
			t.Errorf("%s: RequiredPerm=%q, attendu PermModerate", id, a.RequiredPerm)
		}
	}
	if find(t, reg, "listing.report").RequiredPerm != listingactions.PermReport {
		t.Errorf("listing.report: RequiredPerm attendu PermReport")
	}
}

// C2 : moderate_hide masque effectivement (exclusion de Search via le store).
func TestModerateHideRestoreThroughActions(t *testing.T) {
	store := newStore(t)
	reg := newRegistry(t, store)
	ctx := middleware.ContextWithUser(context.Background(), &identity.User{ID: "mod"})

	l := &listing.Listing{OwnerID: "seller", Title: "Yacht visible", Status: listing.StatusPublished}
	if err := store.Create(context.Background(), l); err != nil {
		t.Fatal(err)
	}

	hide := find(t, reg, "listing.moderate_hide")
	if res, err := hide.Run(ctx, app.AppDeps{}, json.RawMessage(`{"listing_id":"`+l.ID+`"}`)); err != nil || res.Status != "ok" {
		t.Fatalf("hide: status=%s err=%v", res.Status, err)
	}
	got, err := store.Search(context.Background(), listing.Filter{Status: listing.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range got {
		if x.ID == l.ID {
			t.Fatal("listing masqué via action toujours visible en Search")
		}
	}

	restore := find(t, reg, "listing.moderate_restore")
	if res, err := restore.Run(ctx, app.AppDeps{}, json.RawMessage(`{"listing_id":"`+l.ID+`"}`)); err != nil || res.Status != "ok" {
		t.Fatalf("restore: status=%s err=%v", res.Status, err)
	}
}
