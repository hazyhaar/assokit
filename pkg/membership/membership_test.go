package membership_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/membership"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func mkUser(t *testing.T, db *sql.DB, id, email string, active int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, email, "x", email, active,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

func TestCreateAndListForUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	s := &membership.Store{DB: db}

	id, err := s.Create(ctx, membership.Membership{
		UserID:      "alice",
		PeriodStart: "2026-01-01",
		PeriodEnd:   "2026-12-31",
		AmountCents: 2500,
		Status:      "active",
		Note:        "cotisation annuelle",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == "" {
		t.Fatal("id vide")
	}

	list, err := s.ListForUser(ctx, "alice")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].AmountCents != 2500 || list[0].Status != "active" {
		t.Fatalf("adhésion inattendue: %+v", list[0])
	}
}

func TestSetStatusAndCurrentStatus(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	s := &membership.Store{DB: db}

	// Période large autour d'aujourd'hui, statut pending au départ.
	id, err := s.Create(ctx, membership.Membership{
		UserID:      "alice",
		PeriodStart: "2000-01-01",
		PeriodEnd:   "2099-12-31",
		Status:      "pending",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Pending → pas d'adhésion active courante.
	if cur, _ := s.CurrentStatus(ctx, "alice"); cur != "none" {
		t.Fatalf("current pending = %q, want none", cur)
	}

	if err := s.SetStatus(ctx, id, "active"); err != nil {
		t.Fatalf("setstatus: %v", err)
	}
	if cur, _ := s.CurrentStatus(ctx, "alice"); cur != "active" {
		t.Fatalf("current = %q, want active", cur)
	}

	// Membre sans adhésion → none.
	mkUser(t, db, "bob", "bob@example.org", 1)
	if cur, _ := s.CurrentStatus(ctx, "bob"); cur != "none" {
		t.Fatalf("current bob = %q, want none", cur)
	}
}

func TestListAllFilter(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "bob", "bob@example.org", 1)
	s := &membership.Store{DB: db}

	_, _ = s.Create(ctx, membership.Membership{UserID: "alice", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31", Status: "active"})
	_, _ = s.Create(ctx, membership.Membership{UserID: "bob", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31", Status: "pending"})

	all, err := s.ListAll(ctx, "")
	if err != nil {
		t.Fatalf("listall: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all len = %d, want 2", len(all))
	}
	actives, err := s.ListAll(ctx, "active")
	if err != nil {
		t.Fatalf("listall active: %v", err)
	}
	if len(actives) != 1 || actives[0].UserID != "alice" {
		t.Fatalf("filtre active inattendu: %+v", actives)
	}
}

func TestValidation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org", 1)
	mkUser(t, db, "ghost", "ghost@example.org", 0)
	s := &membership.Store{DB: db}

	if _, err := s.Create(ctx, membership.Membership{UserID: "nope", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31"}); !errors.Is(err, membership.ErrUserInvalid) {
		t.Fatalf("user inexistant: err=%v, want ErrUserInvalid", err)
	}
	if _, err := s.Create(ctx, membership.Membership{UserID: "ghost", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31"}); !errors.Is(err, membership.ErrUserInvalid) {
		t.Fatalf("user inactif: err=%v, want ErrUserInvalid", err)
	}
	if _, err := s.Create(ctx, membership.Membership{UserID: "alice", PeriodStart: "2026-12-31", PeriodEnd: "2026-01-01"}); !errors.Is(err, membership.ErrPeriodInvalid) {
		t.Fatalf("période invalide: err=%v, want ErrPeriodInvalid", err)
	}
	if _, err := s.Create(ctx, membership.Membership{UserID: "alice", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31", Status: "bogus"}); !errors.Is(err, membership.ErrStatusInvalid) {
		t.Fatalf("statut invalide: err=%v, want ErrStatusInvalid", err)
	}
	if err := s.SetStatus(ctx, "whatever", "bogus"); !errors.Is(err, membership.ErrStatusInvalid) {
		t.Fatalf("setstatus invalide: err=%v, want ErrStatusInvalid", err)
	}
	if err := s.SetStatus(ctx, "missing", "active"); !errors.Is(err, membership.ErrNotFound) {
		t.Fatalf("setstatus introuvable: err=%v, want ErrNotFound", err)
	}
}
