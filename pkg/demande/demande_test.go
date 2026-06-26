package demande_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/demande"
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

// TestCreateAndListForUser : une demande valide est enregistrée puis relue par son
// propriétaire, avec ses champs et le statut initial « soumise » forcé serveur.
func TestCreateAndListForUser(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u1", "u1@example.com", 1)
	store := &demande.Store{DB: db}

	id, err := store.Create(ctx, demande.Demande{
		UserID:      "u1",
		ParcelleRef: "05015 · AB · 0123",
		Objet:       "Pâturage ovin estival",
		Details:     "Estive du 1er juin au 30 septembre.",
		Statut:      "convertie", // tentative d'injection : doit être ignorée.
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
		t.Fatalf("attendu 1 demande, obtenu %d", len(list))
	}
	d := list[0]
	if d.ParcelleRef != "05015 · AB · 0123" || d.Objet != "Pâturage ovin estival" {
		t.Fatalf("champs relus incorrects: %+v", d)
	}
	if d.Statut != demande.StatutSoumise {
		t.Fatalf("statut initial attendu 'soumise' (forcé serveur), obtenu %q", d.Statut)
	}
}

// TestCreate_ParcelleRefManquante : une référence vide est rejetée, rien n'est créé.
func TestCreate_ParcelleRefManquante(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u2", "u2@example.com", 1)
	store := &demande.Store{DB: db}
	_, err := store.Create(context.Background(), demande.Demande{
		UserID: "u2", ParcelleRef: "  ", Objet: "Pâturage",
	})
	if !errors.Is(err, demande.ErrParcelleRefManquante) {
		t.Fatalf("attendu ErrParcelleRefManquante, obtenu %v", err)
	}
}

// TestCreate_ObjetManquant : un objet vide est rejeté.
func TestCreate_ObjetManquant(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u3", "u3@example.com", 1)
	store := &demande.Store{DB: db}
	_, err := store.Create(context.Background(), demande.Demande{
		UserID: "u3", ParcelleRef: "05015 · AB · 0123", Objet: "  ",
	})
	if !errors.Is(err, demande.ErrObjetManquant) {
		t.Fatalf("attendu ErrObjetManquant, obtenu %v", err)
	}
}

// TestCreate_UserInactif : un membre inactif ne peut pas soumettre de demande.
func TestCreate_UserInactif(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u4", "u4@example.com", 0)
	store := &demande.Store{DB: db}
	_, err := store.Create(context.Background(), demande.Demande{
		UserID: "u4", ParcelleRef: "05015 · AB · 0123", Objet: "Pâturage",
	})
	if !errors.Is(err, demande.ErrUserInvalid) {
		t.Fatalf("attendu ErrUserInvalid, obtenu %v", err)
	}
}

// TestListForUser_IsolationCrossMembre : un membre ne voit que SES demandes, jamais
// celles d'un autre (garde de régression IDOR V3c). La vue admin ListAll voit les deux.
func TestListForUser_IsolationCrossMembre(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "uA", "ua@example.com", 1)
	mkUser(t, db, "uB", "ub@example.com", 1)
	store := &demande.Store{DB: db}

	idA, err := store.Create(ctx, demande.Demande{UserID: "uA", ParcelleRef: "r1", Objet: "Pâturage A"})
	if err != nil {
		t.Fatalf("Create uA: %v", err)
	}
	idB, err := store.Create(ctx, demande.Demande{UserID: "uB", ParcelleRef: "r2", Objet: "Pâturage B"})
	if err != nil {
		t.Fatalf("Create uB: %v", err)
	}

	listA, err := store.ListForUser(ctx, "uA")
	if err != nil {
		t.Fatalf("ListForUser uA: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != idA {
		t.Fatalf("uA: attendu 1 demande id %q, obtenu %+v", idA, listA)
	}
	if listA[0].ID == idB {
		t.Fatalf("uA: fuite cross-membre, demande de uB visible")
	}

	listB, err := store.ListForUser(ctx, "uB")
	if err != nil {
		t.Fatalf("ListForUser uB: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != idB {
		t.Fatalf("uB: attendu 1 demande id %q, obtenu %+v", idB, listB)
	}

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll: attendu 2 demandes, obtenu %d", len(all))
	}
}

// TestSetStatut_GardeReconversion : une demande convertie (ou refusée) est terminale,
// toute nouvelle transition est rejetée par ErrTransitionInterdite.
func TestSetStatut_GardeReconversion(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u5", "u5@example.com", 1)
	store := &demande.Store{DB: db}
	id, err := store.Create(ctx, demande.Demande{UserID: "u5", ParcelleRef: "r1", Objet: "Pâturage"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetStatut(ctx, id, demande.StatutConvertie); err != nil {
		t.Fatalf("SetStatut convertie: %v", err)
	}
	d, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if d.Statut != demande.StatutConvertie {
		t.Fatalf("attendu statut convertie, obtenu %q", d.Statut)
	}
	// Reconversion : interdite.
	if err := store.SetStatut(ctx, id, demande.StatutConvertie); !errors.Is(err, demande.ErrTransitionInterdite) {
		t.Fatalf("reconversion : attendu ErrTransitionInterdite, obtenu %v", err)
	}
	if err := store.SetStatut(ctx, id, demande.StatutRefusee); !errors.Is(err, demande.ErrTransitionInterdite) {
		t.Fatalf("refus après conversion : attendu ErrTransitionInterdite, obtenu %v", err)
	}
}

// TestSetStatut_Atomicite : la garde de transition repose sur la clause WHERE de
// l'UPDATE, pas sur un pré-SELECT. Une première conversion réussit ; toute seconde
// transition (même valeur) est rejetée par ErrTransitionInterdite, car l'UPDATE
// n'affecte plus aucune ligne (statut déjà terminal). Cela protège contre deux POST
// concurrents qui, sur un simple pré-SELECT, auraient pu créer deux conventions.
func TestSetStatut_Atomicite(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u6", "u6@example.com", 1)
	store := &demande.Store{DB: db}
	id, err := store.Create(ctx, demande.Demande{UserID: "u6", ParcelleRef: "r1", Objet: "Pâturage"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Première conversion : réussit.
	if err := store.SetStatut(ctx, id, demande.StatutConvertie); err != nil {
		t.Fatalf("première conversion: %v", err)
	}
	// Seconde conversion : rejetée par la garde SQL (0 ligne affectée), pas par un
	// pré-SELECT. Le statut terminal est donc protégé atomiquement.
	if err := store.SetStatut(ctx, id, demande.StatutConvertie); !errors.Is(err, demande.ErrTransitionInterdite) {
		t.Fatalf("seconde conversion : attendu ErrTransitionInterdite, obtenu %v", err)
	}
	// Le statut reste convertie, aucune double mutation.
	d, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if d.Statut != demande.StatutConvertie {
		t.Fatalf("attendu statut convertie inchangé, obtenu %q", d.Statut)
	}
}

// TestSetStatut_NotFound : un identifiant inconnu retourne ErrNotFound.
func TestSetStatut_NotFound(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := &demande.Store{DB: db}
	if err := store.SetStatut(context.Background(), "inconnu", demande.StatutAcceptee); !errors.Is(err, demande.ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
}
