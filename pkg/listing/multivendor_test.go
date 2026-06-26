package listing_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/messaging"
)

// TestOwnerRequired vérifie C1 : Create exige OwnerID.
func TestOwnerRequired(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	l := listing.Listing{Silo: "yachts", Title: "Sans owner"}
	if err := s.Create(ctx, &l); !errors.Is(err, listing.ErrOwnerRequired) {
		t.Fatalf("attendu ErrOwnerRequired, obtenu %v", err)
	}
}

// TestSearchByOwner vérifie C1 : Search filtrable par owner et persistance OwnerID.
func TestSearchByOwner(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	insertFixtures(t, s) // tous ownerBroker
	other := listing.Listing{
		Silo:    "yachts",
		OwnerID: "owner-particulier",
		Title:   "Lagoon 42 vendu par un particulier",
		Body:    "Catamaran Lagoon, vente directe propriétaire, Guadeloupe.",
		Attrs:   map[string]any{"marque": "Lagoon", "type_coque": "catamaran"},
	}
	if err := s.Create(ctx, &other); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	res, err := s.Search(ctx, listing.Filter{Owner: ownerBroker, Limit: 100})
	if err != nil {
		t.Fatalf("Search owner: %v", err)
	}
	if len(res) != len(yachtsFixtures) {
		t.Fatalf("attendu %d listings du broker, obtenu %d", len(yachtsFixtures), len(res))
	}
	for _, r := range res {
		if r.OwnerID != ownerBroker {
			t.Errorf("listing d'un autre owner dans le filtre: %s", r.OwnerID)
		}
	}

	res2, err := s.Search(ctx, listing.Filter{Owner: "owner-particulier", Limit: 100})
	if err != nil {
		t.Fatalf("Search owner2: %v", err)
	}
	if len(res2) != 1 || res2[0].OwnerID != "owner-particulier" {
		t.Fatalf("attendu 1 listing du particulier, obtenu %d", len(res2))
	}
}

// TestUpdateOwnerScoped vérifie C2 : un NON-owner est rejeté ; l'owner édite.
func TestUpdateOwnerScoped(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	l := listing.Listing{Silo: "yachts", OwnerID: ownerBroker, Title: "Avant", PriceCents: 100}
	if err := s.Create(ctx, &l); err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "Après édition"
	if _, err := s.Update(ctx, "intrus", l.ID, listing.Patch{Title: &newTitle}); !errors.Is(err, listing.ErrNotOwner) {
		t.Fatalf("C2: non-owner devait être rejeté par ErrNotOwner, obtenu %v", err)
	}

	updated, err := s.Update(ctx, ownerBroker, l.ID, listing.Patch{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update owner: %v", err)
	}
	if updated.Title != newTitle {
		t.Fatalf("titre non mis à jour: %s", updated.Title)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Fatalf("UpdatedAt non bumpé")
	}
}

// TestStatusTransitions vérifie C3 : transitions légales acceptées, illégale rejetée.
func TestStatusTransitions(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	l := listing.Listing{Silo: "yachts", OwnerID: ownerBroker, Title: "Cycle de vie", Status: listing.StatusDraft}
	if err := s.Create(ctx, &l); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// draft → published (légal)
	if _, err := s.SetStatus(ctx, ownerBroker, l.ID, listing.StatusPublished); err != nil {
		t.Fatalf("draft→published devait être légal: %v", err)
	}
	// published → sold (légal)
	if _, err := s.SetStatus(ctx, ownerBroker, l.ID, listing.StatusSold); err != nil {
		t.Fatalf("published→sold devait être légal: %v", err)
	}
	// sold → published (ILLÉGAL — état terminal)
	if _, err := s.SetStatus(ctx, ownerBroker, l.ID, listing.StatusPublished); !errors.Is(err, listing.ErrIllegalTransition) {
		t.Fatalf("C3: sold→published devait être rejeté par ErrIllegalTransition, obtenu %v", err)
	}
	// archived→published aussi illégal (sur un autre listing)
	l2 := listing.Listing{Silo: "yachts", OwnerID: ownerBroker, Title: "Archivable", Status: listing.StatusPublished}
	if err := s.Create(ctx, &l2); err != nil {
		t.Fatalf("Create l2: %v", err)
	}
	if _, err := s.SetStatus(ctx, ownerBroker, l2.ID, listing.StatusArchived); err != nil {
		t.Fatalf("published→archived devait être légal: %v", err)
	}
	if _, err := s.SetStatus(ctx, ownerBroker, l2.ID, listing.StatusPublished); !errors.Is(err, listing.ErrIllegalTransition) {
		t.Fatalf("archived→published devait être rejeté, obtenu %v", err)
	}
}

// TestMediasRoundTrip vérifie C4 : liste ordonnée persistée et rendue.
func TestMediasRoundTrip(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	medias := []string{
		"https://cdn.example/neel43/1.jpg",
		"https://cdn.example/neel43/2.jpg",
		"https://cdn.example/neel43/pont.jpg",
	}
	l := listing.Listing{Silo: "yachts", OwnerID: ownerBroker, Title: "Avec médias", Medias: medias}
	if err := s.Create(ctx, &l); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(ctx, l.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Medias) != len(medias) {
		t.Fatalf("attendu %d médias, obtenu %d", len(medias), len(got.Medias))
	}
	for i := range medias {
		if got.Medias[i] != medias[i] {
			t.Fatalf("ordre médias rompu à l'index %d: %q != %q", i, got.Medias[i], medias[i])
		}
	}

	// Update remplace la liste ordonnée
	newMedias := []string{"https://cdn.example/neel43/cale.jpg", "https://cdn.example/neel43/1.jpg"}
	upd, err := s.Update(ctx, ownerBroker, l.ID, listing.Patch{Medias: newMedias})
	if err != nil {
		t.Fatalf("Update medias: %v", err)
	}
	if len(upd.Medias) != 2 || upd.Medias[0] != newMedias[0] {
		t.Fatalf("médias non remplacés correctement: %v", upd.Medias)
	}
}

// usersDB ajoute la table users (réelle, schéma messaging) et y insère deux comptes.
func usersDB(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY, email TEXT, display_name TEXT, is_active INTEGER NOT NULL DEFAULT 1
	)`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY, sender_id TEXT, recipient_id TEXT,
		body_md TEXT, body_html TEXT, created_at TEXT, read_at TEXT
	)`); err != nil {
		t.Fatalf("create messages: %v", err)
	}
	for _, u := range []struct{ id, name string }{
		{ownerBroker, "Bateaux Antilles"},
		{"buyer-claire", "Claire Acheteuse"},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users(id,email,display_name,is_active) VALUES(?,?,?,1)`,
			u.id, u.id+"@example.test", u.name); err != nil {
			t.Fatalf("insert user %s: %v", u.id, err)
		}
	}
}

// TestInquiryIdempotent vérifie C5 : Conversation créée acheteur↔owner référençant
// le listing ; un 2e appel ne duplique pas (idempotence).
func TestInquiryIdempotent(t *testing.T) {
	ctx := context.Background()
	// DB partagée listing + messaging (même instance = une communauté).
	silo, err := listing.LoadSilo(siloPath(t))
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	db := openTestDB(t)
	usersDB(t, db)
	s, err := listing.NewStore(ctx, db, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	msgr := &messaging.Store{DB: db}

	l := listing.Listing{Silo: "yachts", OwnerID: ownerBroker, Title: "NEEL 43 à vendre"}
	if err := s.Create(ctx, &l); err != nil {
		t.Fatalf("Create: %v", err)
	}

	inq1, err := s.OpenInquiry(ctx, msgr, l.ID, "buyer-claire", "Le bateau est-il toujours disponible ?")
	if err != nil {
		t.Fatalf("OpenInquiry 1: %v", err)
	}
	if inq1.OwnerID != ownerBroker || inq1.BuyerID != "buyer-claire" || inq1.ListingID != l.ID {
		t.Fatalf("inquiry mal référencée: %+v", inq1)
	}

	inq2, err := s.OpenInquiry(ctx, msgr, l.ID, "buyer-claire", "Encore disponible ?")
	if err != nil {
		t.Fatalf("OpenInquiry 2: %v", err)
	}
	if inq2.MessageID != inq1.MessageID {
		t.Fatalf("C5: idempotence rompue, message dupliqué %s != %s", inq2.MessageID, inq1.MessageID)
	}

	// Un seul message dans le thread (pas de doublon).
	thread, err := msgr.Thread(ctx, "buyer-claire", ownerBroker, 100)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(thread) != 1 {
		t.Fatalf("attendu 1 message (idempotent), obtenu %d", len(thread))
	}

	// L'owner ne peut pas s'enquérir de son propre listing.
	if _, err := s.OpenInquiry(ctx, msgr, l.ID, ownerBroker, "test"); !errors.Is(err, listing.ErrSelfInquiry) {
		t.Fatalf("attendu ErrSelfInquiry, obtenu %v", err)
	}
}
