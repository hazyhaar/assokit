package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/rbac"
)

// stubMailer enregistre les Enqueue sans SMTP — preuve d'envoi en test.
type stubMailer struct {
	mu   sync.Mutex
	sent []string // adresses destinataires, dans l'ordre d'envoi
}

func (m *stubMailer) Enqueue(_ context.Context, to, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to)
	return nil
}

func (m *stubMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func newSiloStore(t *testing.T) *listing.Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "silo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := listing.NewStore(context.Background(), db, &listing.SiloDef{ID: "yachts", Label: "Yachts"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newCoreWithUsers(t *testing.T, users map[string]string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	for id, email := range users {
		if _, err := db.Exec(
			`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES(?,?,?,?,1)`,
			id, email, "x", email); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// C3 (décisif) : à la publication, l'owner d'une recherche sauvée matchante
// reçoit un Enqueue ; un non-match ne reçoit RIEN ; un re-run ne ré-envoie PAS.
func TestAlertWorkerMatchAndIdempotence(t *testing.T) {
	ctx := context.Background()
	store := newSiloStore(t)
	core := newCoreWithUsers(t, map[string]string{
		"watcher":   "watcher@example.org",
		"unrelated": "unrelated@example.org",
	})

	// watcher veut tout "catamaran" ; unrelated veut "monohull".
	if _, err := store.SaveSearch(ctx, "watcher", "cata", listing.Filter{Text: "catamaran"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSearch(ctx, "unrelated", "mono", listing.Filter{Text: "monohull"}); err != nil {
		t.Fatal(err)
	}

	// Une annonce publiée qui matche "catamaran".
	l := &listing.Listing{OwnerID: "seller", Title: "Beau catamaran 12m", Status: listing.StatusPublished}
	if err := store.Create(ctx, l); err != nil {
		t.Fatal(err)
	}

	mlr := &stubMailer{}
	w, err := newAlertWorker(ctx, store, mlr, core, "yachts", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("newAlertWorker: %v", err)
	}

	n, err := w.scanOnce(ctx)
	if err != nil {
		t.Fatalf("scanOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("1er scan: %d alertes émises, attendu 1", n)
	}
	if mlr.count() != 1 {
		t.Fatalf("1 enqueue attendu, obtenu %d (%v)", mlr.count(), mlr.sent)
	}
	if mlr.sent[0] != "watcher@example.org" {
		t.Fatalf("destinataire incorrect: %s (le non-match ne doit RIEN recevoir)", mlr.sent[0])
	}

	// Re-run : idempotence — aucune nouvelle alerte.
	n2, err := w.scanOnce(ctx)
	if err != nil {
		t.Fatalf("2e scanOnce: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-run: %d alertes ré-émises, attendu 0 (idempotence)", n2)
	}
	if mlr.count() != 1 {
		t.Fatalf("re-run a ré-enqueue: total %d, attendu 1", mlr.count())
	}
}

// C2/C4 : la perm de modération n'est accordée qu'au grade modérateur. Un membre
// est REJETÉ par Can() ; un modérateur est autorisé.
func TestModeratePermNotGrantedToMember(t *testing.T) {
	ctx := context.Background()
	core := newCoreWithUsers(t, map[string]string{
		"m":   "member@example.org",
		"mod": "mod@example.org",
	})

	// Câblage RBAC exactement comme la bordure run().
	if err := grantListingPermsToMember(ctx, core); err != nil {
		t.Fatalf("grant member: %v", err)
	}
	if err := grantModeratePermsToModerator(ctx, core); err != nil {
		t.Fatalf("grant moderator: %v", err)
	}

	svc := &rbac.Service{Store: &rbac.Store{DB: core}, Cache: &rbac.Cache{}}
	if err := svc.AssignGrade(ctx, "m", "sys-member"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignGrade(ctx, "mod", "sys-moderator"); err != nil {
		t.Fatal(err)
	}

	// Un membre PEUT signaler mais NE PEUT PAS modérer.
	if ok, err := svc.Can(ctx, "m", "listing.report"); err != nil || !ok {
		t.Fatalf("membre devrait pouvoir report: ok=%v err=%v", ok, err)
	}
	if ok, err := svc.Can(ctx, "m", "listing.moderate"); err != nil || ok {
		t.Fatalf("membre NE doit PAS pouvoir modérer: ok=%v err=%v", ok, err)
	}
	// Un modérateur PEUT modérer.
	if ok, err := svc.Can(ctx, "mod", "listing.moderate"); err != nil || !ok {
		t.Fatalf("modérateur devrait pouvoir modérer: ok=%v err=%v", ok, err)
	}
}
