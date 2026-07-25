package parcelle_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
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

func mkParcelle(t *testing.T, s *parcelle.Store, commune, section, numero string) string {
	t.Helper()
	id, err := s.Create(context.Background(), parcelle.Parcelle{
		CommuneCode:    commune,
		Section:        section,
		NumeroParcelle: numero,
		SurfaceM2:      12000,
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
	s := &parcelle.Store{DB: db}
	ctx := context.Background()

	id := mkParcelle(t, s, "05015", "AB", "0123")
	if id == "" {
		t.Fatal("id vide")
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Parcelle.CommuneCode != "05015" || got.Parcelle.Section != "AB" || got.Parcelle.NumeroParcelle != "0123" {
		t.Fatalf("round-trip incohérent: %+v", got.Parcelle)
	}
	if got.Parcelle.SurfaceM2 != 12000 || got.Parcelle.StatutMad != "mise_a_disposition" {
		t.Fatalf("champs incohérents: %+v", got.Parcelle)
	}
}

func TestCreateDefaultStatut(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	id, err := s.Create(context.Background(), parcelle.Parcelle{
		CommuneCode: "05015", Section: "AC", NumeroParcelle: "0001",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.Get(context.Background(), id)
	if got.Parcelle.StatutMad != "libre" {
		t.Fatalf("statut par défaut attendu 'libre', obtenu %q", got.Parcelle.StatutMad)
	}
}

func TestCreateChampManquant(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	_, err := s.Create(context.Background(), parcelle.Parcelle{CommuneCode: "05015"})
	if !errors.Is(err, parcelle.ErrChampManquant) {
		t.Fatalf("attendu ErrChampManquant, obtenu %v", err)
	}
}

func TestCreateStatutInvalide(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	_, err := s.Create(context.Background(), parcelle.Parcelle{
		CommuneCode: "05015", Section: "AB", NumeroParcelle: "0123",
		StatutMad: "squat",
	})
	if !errors.Is(err, parcelle.ErrStatutInvalide) {
		t.Fatalf("attendu ErrStatutInvalide, obtenu %v", err)
	}
	// Aucune parcelle ne doit avoir été créée.
	all, _ := s.ListAll(context.Background())
	if len(all) != 0 {
		t.Fatalf("aucune parcelle ne doit être créée sur statut invalide, obtenu %d", len(all))
	}
}

func TestCreateSurfaceNegative(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	_, err := s.Create(context.Background(), parcelle.Parcelle{
		CommuneCode: "05015", Section: "AB", NumeroParcelle: "0123",
		SurfaceM2: -1, StatutMad: "libre",
	})
	if !errors.Is(err, parcelle.ErrSurfaceInvalide) {
		t.Fatalf("attendu ErrSurfaceInvalide, obtenu %v", err)
	}
}

func TestUniqueConstraint(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	mkParcelle(t, s, "05015", "AB", "0123")
	_, err := s.Create(context.Background(), parcelle.Parcelle{
		CommuneCode: "05015", Section: "AB", NumeroParcelle: "0123",
	})
	if !errors.Is(err, parcelle.ErrParcelleExists) {
		t.Fatalf("attendu ErrParcelleExists, obtenu %v", err)
	}
}

func TestAddDroitNatureInvalide(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	mkUser(t, db, "alice")
	id := mkParcelle(t, s, "05015", "AB", "0123")
	_, err := s.AddDroit(context.Background(), id, "alice", "locataire")
	if !errors.Is(err, parcelle.ErrNatureInvalide) {
		t.Fatalf("attendu ErrNatureInvalide, obtenu %v", err)
	}
}

func TestAddDroitParcelleInconnue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	mkUser(t, db, "alice")
	_, err := s.AddDroit(context.Background(), "inexistant", "alice", "occupant")
	if !errors.Is(err, parcelle.ErrParcelleNotFound) {
		t.Fatalf("attendu ErrParcelleNotFound, obtenu %v", err)
	}
}

func TestAddDroitIdempotent(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	ctx := context.Background()
	mkUser(t, db, "alice")
	id := mkParcelle(t, s, "05015", "AB", "0300")

	first, err := s.AddDroit(ctx, id, "alice", "pleine_propriete")
	if err != nil {
		t.Fatalf("AddDroit initial: %v", err)
	}
	replay, err := s.AddDroit(ctx, id, "alice", "usufruit")
	if err != nil {
		t.Fatalf("AddDroit rejeu: %v (le rejeu ne doit jamais remonter d'erreur UNIQUE)", err)
	}
	if replay != first {
		t.Errorf("AddDroit rejeu: id=%s, want id existant %s", replay, first)
	}
	var n int
	var nature string
	if err := db.QueryRow(`SELECT COUNT(*) FROM parcelle_droits WHERE parcelle_id=?`, id).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("droits = %d, want 1 (pas de doublon)", n)
	}
	if err := db.QueryRow(`SELECT nature_du_droit FROM parcelle_droits WHERE id=?`, first).Scan(&nature); err != nil {
		t.Fatalf("nature: %v", err)
	}
	if nature != "pleine_propriete" {
		t.Errorf("nature = %s, want pleine_propriete (nature d'origine conservée)", nature)
	}
}

// TestRemoveDroitsForUser : la purge retire tous les droits d'un membre, ne
// touche pas ceux d'autrui, et est idempotente (un membre sans droit → 0).
func TestRemoveDroitsForUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	ctx := context.Background()
	mkUser(t, db, "alice")
	mkUser(t, db, "bob")
	p1 := mkParcelle(t, s, "05015", "AB", "0400")
	p2 := mkParcelle(t, s, "05015", "AB", "0401")
	if _, err := s.AddDroit(ctx, p1, "alice", "pleine_propriete"); err != nil {
		t.Fatalf("AddDroit p1 alice: %v", err)
	}
	if _, err := s.AddDroit(ctx, p2, "alice", "usufruit"); err != nil {
		t.Fatalf("AddDroit p2 alice: %v", err)
	}
	if _, err := s.AddDroit(ctx, p1, "bob", "occupant"); err != nil {
		t.Fatalf("AddDroit p1 bob: %v", err)
	}

	n, err := s.RemoveDroitsForUser(ctx, "alice")
	if err != nil {
		t.Fatalf("RemoveDroitsForUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("droits supprimés = %d, want 2", n)
	}
	var nAlice, nBob int
	db.QueryRow(`SELECT COUNT(*) FROM parcelle_droits WHERE user_id=?`, "alice").Scan(&nAlice)
	db.QueryRow(`SELECT COUNT(*) FROM parcelle_droits WHERE user_id=?`, "bob").Scan(&nBob)
	if nAlice != 0 || nBob != 1 {
		t.Fatalf("après purge alice: alice=%d bob=%d, want 0/1", nAlice, nBob)
	}

	// Idempotence : re-purger alice → 0, sans erreur.
	if again, err := s.RemoveDroitsForUser(ctx, "alice"); err != nil || again != 0 {
		t.Fatalf("purge idempotente: n=%d err=%v", again, err)
	}
}

// TestListForUserOwnership vérifie le filtrage d'appartenance : un membre B ne
// voit jamais les parcelles d'un membre A.
func TestListForUserOwnership(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	s := &parcelle.Store{DB: db}
	ctx := context.Background()
	mkUser(t, db, "alice")
	mkUser(t, db, "bob")

	pAlice := mkParcelle(t, s, "05015", "AB", "0100")
	pBob := mkParcelle(t, s, "05015", "AB", "0200")

	if _, err := s.AddDroit(ctx, pAlice, "alice", "pleine_propriete"); err != nil {
		t.Fatalf("droit alice: %v", err)
	}
	if _, err := s.AddDroit(ctx, pBob, "bob", "usufruit"); err != nil {
		t.Fatalf("droit bob: %v", err)
	}

	aliceList, err := s.ListForUser(ctx, "alice")
	if err != nil {
		t.Fatalf("list alice: %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != pAlice {
		t.Fatalf("alice devrait voir exactement sa parcelle, obtenu %+v", aliceList)
	}
	for _, p := range aliceList {
		if p.ID == pBob {
			t.Fatal("fuite : alice voit la parcelle de bob")
		}
	}

	bobList, err := s.ListForUser(ctx, "bob")
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(bobList) != 1 || bobList[0].ID != pBob {
		t.Fatalf("bob devrait voir exactement sa parcelle, obtenu %+v", bobList)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll devrait renvoyer 2 parcelles (vue bureau), obtenu %d", len(all))
	}
}
