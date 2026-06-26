package events_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/events"
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

func mkUser(t *testing.T, db *sql.DB, id, email string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,1)`,
		id, email, "x", email,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

func TestCreateGetAndSlug(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "alice", "alice@example.org")
	s := &events.Store{DB: db}

	id, err := s.Create(ctx, events.Event{
		Title:     "Reunion de rentree",
		DescMD:    "Ordre du jour **important**.",
		Location:  "Salle A",
		StartsAt:  "2026-09-01 18:00:00",
		CreatedBy: sql.NullString{String: "alice", Valid: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	e, err := s.Get(ctx, "reunion-de-rentree")
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if e.ID != id {
		t.Fatalf("get id = %q, want %q", e.ID, id)
	}
	if e.DescHTML == "" || e.DescHTML == e.DescMD {
		t.Fatalf("description_html non rendu: %q", e.DescHTML)
	}

	// Get par id aussi.
	if _, err := s.Get(ctx, id); err != nil {
		t.Fatalf("get by id: %v", err)
	}
}

func TestCreateValidationAndSlugTaken(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &events.Store{DB: db}

	if _, err := s.Create(ctx, events.Event{StartsAt: "2026-01-01 10:00:00"}); !errors.Is(err, events.ErrEmptyTitle) {
		t.Fatalf("empty title: err=%v, want ErrEmptyTitle", err)
	}
	if _, err := s.Create(ctx, events.Event{Title: "Sans date"}); !errors.Is(err, events.ErrEmptyStart) {
		t.Fatalf("empty start: err=%v, want ErrEmptyStart", err)
	}
	if _, err := s.Create(ctx, events.Event{Title: "Fête", Slug: "fete", StartsAt: "2026-01-01 10:00:00"}); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := s.Create(ctx, events.Event{Title: "Autre", Slug: "fete", StartsAt: "2026-02-01 10:00:00"}); !errors.Is(err, events.ErrSlugTaken) {
		t.Fatalf("slug taken: err=%v, want ErrSlugTaken", err)
	}
}

func TestListOrderAndSoftDelete(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &events.Store{DB: db}

	// Un passé, un futur lointain, un futur proche.
	if _, err := s.Create(ctx, events.Event{Title: "Passé", StartsAt: "2020-01-01 10:00:00"}); err != nil {
		t.Fatalf("c1: %v", err)
	}
	if _, err := s.Create(ctx, events.Event{Title: "Lointain", StartsAt: "2099-01-01 10:00:00"}); err != nil {
		t.Fatalf("c2: %v", err)
	}
	idClose, err := s.Create(ctx, events.Event{Title: "Proche", StartsAt: "2030-01-01 10:00:00"})
	if err != nil {
		t.Fatalf("c3: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}
	// À venir d'abord, le plus proche en tête.
	if list[0].Title != "Proche" || list[1].Title != "Lointain" || list[2].Title != "Passé" {
		t.Fatalf("ordre inattendu: %s, %s, %s", list[0].Title, list[1].Title, list[2].Title)
	}

	if err := s.SoftDelete(ctx, idClose); err != nil {
		t.Fatalf("softdelete: %v", err)
	}
	list, _ = s.List(ctx)
	if len(list) != 2 {
		t.Fatalf("list après delete = %d, want 2", len(list))
	}
	if _, err := s.Get(ctx, idClose); !errors.Is(err, events.ErrNotFound) {
		t.Fatalf("get après delete: err=%v, want ErrNotFound", err)
	}
	if err := s.SoftDelete(ctx, idClose); !errors.Is(err, events.ErrNotFound) {
		t.Fatalf("double delete: err=%v, want ErrNotFound", err)
	}
}

func TestUpdate(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	s := &events.Store{DB: db}

	id, err := s.Create(ctx, events.Event{Title: "Brouillon", StartsAt: "2030-01-01 10:00:00"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	err = s.Update(ctx, events.Event{ID: id, Title: "Final", DescMD: "ok", StartsAt: "2030-02-01 10:00:00"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	e, _ := s.Get(ctx, id)
	if e.Title != "Final" || e.StartsAt != "2030-02-01 10:00:00" {
		t.Fatalf("update non appliqué: %+v", e)
	}
	if err := s.Update(ctx, events.Event{ID: "inconnu", Title: "X", StartsAt: "2030-01-01 10:00:00"}); !errors.Is(err, events.ErrNotFound) {
		t.Fatalf("update inconnu: err=%v, want ErrNotFound", err)
	}
}
