package convention_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/parcelle"
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

func mkUser(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,?)`,
		id, id+"@example.org", "x", id, 1,
	)
	if err != nil {
		t.Fatalf("mkUser %s: %v", id, err)
	}
}

func mkParcelle(t *testing.T, db *sql.DB, commune, section, numero string) string {
	t.Helper()
	id, err := (&parcelle.Store{DB: db}).Create(context.Background(), parcelle.Parcelle{
		CommuneCode:    commune,
		Section:        section,
		NumeroParcelle: numero,
		SurfaceM2:      8000,
		StatutMad:      "mise_a_disposition",
	})
	if err != nil {
		t.Fatalf("create parcelle: %v", err)
	}
	return id
}

func TestCreateRoundTrip(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "preneur-1")
	p1 := mkParcelle(t, db, "05015", "AB", "0001")
	p2 := mkParcelle(t, db, "05015", "AB", "0002")

	s := &convention.Store{DB: db}
	id, err := s.Create(ctx, convention.Convention{
		PreneurID: "preneur-1",
		Parcelles: []string{p1, p2, p1}, // doublon dédupliqué
		DureeMois: 36,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PreneurID != "preneur-1" || got.DureeMois != 36 || got.Statut != "brouillon" {
		t.Fatalf("convention inattendue: %+v", got)
	}
	if len(got.Parcelles) != 2 {
		t.Fatalf("parcelles attendues 2, obtenu %d (%v)", len(got.Parcelles), got.Parcelles)
	}
}

func TestCreateValidation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "preneur-1")
	p1 := mkParcelle(t, db, "05015", "AB", "0001")
	s := &convention.Store{DB: db}

	for _, tc := range []struct {
		name string
		conv convention.Convention
		want error
	}{
		{"preneur vide", convention.Convention{Parcelles: []string{p1}}, convention.ErrPreneurManquant},
		{"sans parcelle", convention.Convention{PreneurID: "preneur-1"}, convention.ErrParcellesManquantes},
		{"statut invalide", convention.Convention{PreneurID: "preneur-1", Parcelles: []string{p1}, Statut: "x"}, convention.ErrStatutInvalide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Create(ctx, tc.conv)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestCreateUnknownParcelleFailsLoud(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "preneur-1")
	s := &convention.Store{DB: db}
	if _, err := s.Create(ctx, convention.Convention{
		PreneurID: "preneur-1",
		Parcelles: []string{"parcelle-inexistante"},
	}); err == nil {
		t.Fatal("une parcelle inconnue doit faire échouer la création (FK)")
	}
}

func TestListForUserIsolation(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "preneur-1")
	mkUser(t, db, "preneur-2")
	p1 := mkParcelle(t, db, "05015", "AB", "0001")
	s := &convention.Store{DB: db}
	if _, err := s.Create(ctx, convention.Convention{PreneurID: "preneur-1", Parcelles: []string{p1}}); err != nil {
		t.Fatalf("create p1: %v", err)
	}

	mine, err := s.ListForUser(ctx, "preneur-1")
	if err != nil {
		t.Fatalf("list preneur-1: %v", err)
	}
	if len(mine) != 1 {
		t.Fatalf("preneur-1 doit voir 1 convention, obtenu %d", len(mine))
	}
	other, err := s.ListForUser(ctx, "preneur-2")
	if err != nil {
		t.Fatalf("list preneur-2: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("preneur-2 ne doit voir aucune convention, obtenu %d", len(other))
	}
}

func TestRevoke(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "preneur-1")
	p1 := mkParcelle(t, db, "05015", "AB", "0001")
	s := &convention.Store{DB: db}
	id, err := s.Create(ctx, convention.Convention{PreneurID: "preneur-1", Parcelles: []string{p1}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Revoke(ctx, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Statut != "revoquee" {
		t.Fatalf("statut attendu revoquee, obtenu %s", got.Statut)
	}
	if err := s.Revoke(ctx, "inconnu"); !errors.Is(err, convention.ErrConventionNotFound) {
		t.Fatalf("revoke inconnu: want ErrConventionNotFound, got %v", err)
	}
}

func TestInertProviderHasNoClause(t *testing.T) {
	clauses, err := convention.InertProvider{}.Clauses(context.Background(), convention.ConventionContext{PreneurID: "x"})
	if err != nil {
		t.Fatalf("inert provider err: %v", err)
	}
	if len(clauses) != 0 {
		t.Fatalf("le fournisseur inerte ne doit fabriquer aucune clause, obtenu %d", len(clauses))
	}
}
