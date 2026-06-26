package listing

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newModStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := NewStore(context.Background(), db, &SiloDef{ID: "test", Label: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// C1 : ReportListing persiste un signalement avec le reporter fourni (du
// contexte côté action), et valide les champs requis.
func TestReportListingPersists(t *testing.T) {
	ctx := context.Background()
	s := newModStore(t)

	l := &Listing{OwnerID: "seller", Title: "Voilier"}
	if err := s.Create(ctx, l); err != nil {
		t.Fatal(err)
	}

	r, err := s.ReportListing(ctx, "reporter-1", l.ID, "contenu trompeur")
	if err != nil {
		t.Fatalf("ReportListing: %v", err)
	}
	if r.ReporterID != "reporter-1" || r.ListingID != l.ID || r.Reason != "contenu trompeur" {
		t.Fatalf("report mal persisté: %+v", r)
	}

	var reporter, reason string
	if err := s.db.QueryRowContext(ctx,
		`SELECT reporter_id, reason FROM listing_reports WHERE id=?`, r.ID).
		Scan(&reporter, &reason); err != nil {
		t.Fatalf("relecture report: %v", err)
	}
	if reporter != "reporter-1" || reason != "contenu trompeur" {
		t.Fatalf("ligne report incorrecte: reporter=%s reason=%s", reporter, reason)
	}

	for _, tc := range []struct {
		name                  string
		reporter, lid, reason string
		want                  error
	}{
		{"no reporter", "", l.ID, "x", ErrReporterRequired},
		{"no listing", "r", "", "x", ErrListingRequired},
		{"no reason", "r", l.ID, "", ErrReasonRequired},
	} {
		if _, err := s.ReportListing(ctx, tc.reporter, tc.lid, tc.reason); err != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// C2 : un listing masqué est EXCLU de Search quel que soit son Status ; Unhide le
// réintègre. hidden ne touche pas le Status vendeur.
func TestHiddenExcludedFromSearchAnyStatus(t *testing.T) {
	ctx := context.Background()

	for _, st := range []Status{StatusDraft, StatusPublished, StatusSold, StatusArchived} {
		t.Run(string(st), func(t *testing.T) {
			s := newModStore(t)
			l := &Listing{OwnerID: "seller", Title: "Catamaran unique " + string(st), Status: st}
			if err := s.Create(ctx, l); err != nil {
				t.Fatal(err)
			}

			// Visible avant masquage (filtré par le même Status).
			pre, err := s.Search(ctx, Filter{Status: st})
			if err != nil {
				t.Fatal(err)
			}
			if !containsID(pre, l.ID) {
				t.Fatalf("listing absent de Search avant masquage (status=%s)", st)
			}

			if err := s.Hide(ctx, l.ID); err != nil {
				t.Fatalf("Hide: %v", err)
			}

			// Exclu après masquage, quel que soit le Status.
			post, err := s.Search(ctx, Filter{Status: st})
			if err != nil {
				t.Fatal(err)
			}
			if containsID(post, l.ID) {
				t.Fatalf("listing masqué TOUJOURS visible en Search (status=%s)", st)
			}
			// Exclu aussi sans filtre de statut.
			all, err := s.Search(ctx, Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if containsID(all, l.ID) {
				t.Fatalf("listing masqué visible en Search sans filtre statut (status=%s)", st)
			}

			// Le Status vendeur est intact (hidden ≠ transition).
			got, err := s.Get(ctx, l.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != st {
				t.Fatalf("Status altéré par Hide: got %s want %s", got.Status, st)
			}

			// Unhide réintègre.
			if err := s.Unhide(ctx, l.ID); err != nil {
				t.Fatalf("Unhide: %v", err)
			}
			back, err := s.Search(ctx, Filter{Status: st})
			if err != nil {
				t.Fatal(err)
			}
			if !containsID(back, l.ID) {
				t.Fatalf("listing non réintégré après Unhide (status=%s)", st)
			}
		})
	}
}

// Hide/Unhide sur un id inexistant remontent ErrNotFound (fail-loud).
func TestHideUnknownListing(t *testing.T) {
	s := newModStore(t)
	if err := s.Hide(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("Hide inconnu: got %v want ErrNotFound", err)
	}
	if err := s.Unhide(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("Unhide inconnu: got %v want ErrNotFound", err)
	}
}

func containsID(ls []Listing, id string) bool {
	for _, l := range ls {
		if l.ID == id {
			return true
		}
	}
	return false
}
