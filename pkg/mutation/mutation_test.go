package mutation_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/mutation"
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

// TestCreateAndListForUser : une déclaration valide est enregistrée puis relue par
// son propriétaire, avec ses champs et le statut initial « declaree ».
func TestCreateAndListForUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u1", "u1@example.com", 1)
	store := &mutation.Store{DB: db}

	id, err := store.Create(ctx, mutation.Declaration{
		UserID:      "u1",
		ParcelleRef: "05015 · AB · 0123",
		Type:        "vente",
		Details:     "Cession à un voisin.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create devrait retourner un id non vide")
	}

	list, err := store.ListForUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("attendu 1 déclaration, obtenu %d", len(list))
	}
	d := list[0]
	if d.ParcelleRef != "05015 · AB · 0123" || d.Type != "vente" || d.Details != "Cession à un voisin." {
		t.Fatalf("champs relus incorrects: %+v", d)
	}
	if d.Status != "declaree" {
		t.Fatalf("statut initial attendu 'declaree', obtenu %q", d.Status)
	}
}

// TestListForUser_Empty : un membre sans déclaration obtient une liste vide.
func TestListForUser_Empty(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u2", "u2@example.com", 1)
	store := &mutation.Store{DB: db}
	list, err := store.ListForUser(context.Background(), "u2")
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("attendu 0 déclaration, obtenu %d", len(list))
	}
}

// TestCreate_TypeInvalide : un type hors énumération est rejeté, rien n'est créé.
func TestCreate_TypeInvalide(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u3", "u3@example.com", 1)
	store := &mutation.Store{DB: db}
	_, err := store.Create(context.Background(), mutation.Declaration{
		UserID:      "u3",
		ParcelleRef: "05015 · AB · 0123",
		Type:        "donation",
	})
	if !errors.Is(err, mutation.ErrTypeInvalide) {
		t.Fatalf("attendu ErrTypeInvalide, obtenu %v", err)
	}
}

// TestCreate_ParcelleRefManquante : une référence vide est rejetée.
func TestCreate_ParcelleRefManquante(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u4", "u4@example.com", 1)
	store := &mutation.Store{DB: db}
	_, err := store.Create(context.Background(), mutation.Declaration{
		UserID:      "u4",
		ParcelleRef: "  ",
		Type:        "vente",
	})
	if !errors.Is(err, mutation.ErrParcelleRefManquante) {
		t.Fatalf("attendu ErrParcelleRefManquante, obtenu %v", err)
	}
}

// TestCreate_UserInactif : un membre inactif ne peut pas déclarer.
func TestCreate_UserInactif(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u5", "u5@example.com", 0)
	store := &mutation.Store{DB: db}
	_, err := store.Create(context.Background(), mutation.Declaration{
		UserID:      "u5",
		ParcelleRef: "05015 · AB · 0123",
		Type:        "succession",
	})
	if !errors.Is(err, mutation.ErrUserInvalid) {
		t.Fatalf("attendu ErrUserInvalid, obtenu %v", err)
	}
}

// TestListForUser_IsolationCrossMembre : un membre ne voit que SES déclarations,
// jamais celles d'un autre (garde de régression IDOR V3a). La vue admin ListAll
// voit en revanche les deux.
func TestListForUser_IsolationCrossMembre(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "uA", "ua@example.com", 1)
	mkUser(t, db, "uB", "ub@example.com", 1)
	store := &mutation.Store{DB: db}

	idA, err := store.Create(ctx, mutation.Declaration{
		UserID:      "uA",
		ParcelleRef: "05015 · AB · 0001",
		Type:        "vente",
	})
	if err != nil {
		t.Fatalf("Create uA: %v", err)
	}
	idB, err := store.Create(ctx, mutation.Declaration{
		UserID:      "uB",
		ParcelleRef: "05015 · CD · 0002",
		Type:        "succession",
	})
	if err != nil {
		t.Fatalf("Create uB: %v", err)
	}

	listA, err := store.ListForUser(ctx, "uA")
	if err != nil {
		t.Fatalf("ListForUser uA: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("uA: attendu 1 déclaration, obtenu %d", len(listA))
	}
	if listA[0].ID != idA {
		t.Fatalf("uA: attendu id %q, obtenu %q", idA, listA[0].ID)
	}
	if listA[0].ID == idB {
		t.Fatalf("uA: fuite cross-membre, déclaration de uB visible")
	}

	listB, err := store.ListForUser(ctx, "uB")
	if err != nil {
		t.Fatalf("ListForUser uB: %v", err)
	}
	if len(listB) != 1 {
		t.Fatalf("uB: attendu 1 déclaration, obtenu %d", len(listB))
	}
	if listB[0].ID != idB {
		t.Fatalf("uB: attendu id %q, obtenu %q", idB, listB[0].ID)
	}
	if listB[0].ID == idA {
		t.Fatalf("uB: fuite cross-membre, déclaration de uA visible")
	}

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll: attendu 2 déclarations, obtenu %d", len(all))
	}
}

// TestListAll : la vue administration voit les déclarations de tous les membres.
func TestListAll(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u6", "u6@example.com", 1)
	mkUser(t, db, "u7", "u7@example.com", 1)
	store := &mutation.Store{DB: db}
	if _, err := store.Create(ctx, mutation.Declaration{UserID: "u6", ParcelleRef: "r1", Type: "vente"}); err != nil {
		t.Fatalf("Create u6: %v", err)
	}
	if _, err := store.Create(ctx, mutation.Declaration{UserID: "u7", ParcelleRef: "r2", Type: "succession"}); err != nil {
		t.Fatalf("Create u7: %v", err)
	}
	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("attendu 2 déclarations, obtenu %d", len(all))
	}
}
