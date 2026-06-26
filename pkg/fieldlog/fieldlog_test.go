package fieldlog_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/fieldlog"
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

// TestCreateEntretienAndRelire : une déclaration d'entretien valide est enregistrée
// puis relue par son propriétaire, champs intacts.
func TestCreateEntretienAndRelire(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u1", "u1@example.com", 1)
	store := &fieldlog.Store{DB: db}

	id, err := store.Create(ctx, fieldlog.Entry{
		UserID:      "u1",
		Kind:        fieldlog.KindEntretien,
		Category:    "cloture",
		ParcelleRef: "09047 · AB · 0012",
		EventDate:   "2026-06-10",
		Details:     "Réfection de 80 m de clôture côté nord.",
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
		t.Fatalf("attendu 1 entrée, obtenu %d", len(list))
	}
	e := list[0]
	if e.Kind != fieldlog.KindEntretien || e.Category != "cloture" || e.ParcelleRef != "09047 · AB · 0012" ||
		e.EventDate != "2026-06-10" || e.Details != "Réfection de 80 m de clôture côté nord." {
		t.Fatalf("champs relus incorrects: %+v", e)
	}
}

// TestCreateProblemeAndRelire : un problème terrain valide est enregistré puis relu.
func TestCreateProblemeAndRelire(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u2", "u2@example.com", 1)
	store := &fieldlog.Store{DB: db}

	id, err := store.Create(ctx, fieldlog.Entry{
		UserID:      "u2",
		Kind:        fieldlog.KindProbleme,
		Category:    "gibier",
		ParcelleRef: "09047 · ZC · 0099",
		EventDate:   "2026-06-12",
		Details:     "Dégâts de cervidés sur le parc bas (Hemlinger).",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create devrait retourner un id non vide")
	}

	list, err := store.ListForUserKind(ctx, "u2", fieldlog.KindProbleme)
	if err != nil {
		t.Fatalf("ListForUserKind: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("attendu 1 problème, obtenu %d", len(list))
	}
	if list[0].Category != "gibier" || list[0].Details != "Dégâts de cervidés sur le parc bas (Hemlinger)." {
		t.Fatalf("champs relus incorrects: %+v", list[0])
	}
}

// TestListForUser_Empty : un membre sans entrée obtient une liste vide.
func TestListForUser_Empty(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u3", "u3@example.com", 1)
	store := &fieldlog.Store{DB: db}
	list, err := store.ListForUser(context.Background(), "u3")
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("attendu 0 entrée, obtenu %d", len(list))
	}
}

// TestCreate_KindInvalide : un type d'entrée inconnu est rejeté, rien n'est créé.
func TestCreate_KindInvalide(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u4", "u4@example.com", 1)
	store := &fieldlog.Store{DB: db}
	_, err := store.Create(context.Background(), fieldlog.Entry{
		UserID:   "u4",
		Kind:     "rapport",
		Category: "cloture",
	})
	if !errors.Is(err, fieldlog.ErrKindInvalide) {
		t.Fatalf("attendu ErrKindInvalide, obtenu %v", err)
	}
}

// TestCreate_CategorieCroisee : une catégorie de problème sur un entretien (et
// inversement) est rejetée. Garantit la cohérence kind × catégorie côté Go,
// au-delà du CHECK global de la table.
func TestCreate_CategorieCroisee(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "u5", "u5@example.com", 1)
	store := &fieldlog.Store{DB: db}

	_, err := store.Create(ctx, fieldlog.Entry{
		UserID:   "u5",
		Kind:     fieldlog.KindEntretien,
		Category: "gibier", // catégorie de problème, interdite en entretien
	})
	if !errors.Is(err, fieldlog.ErrCategorieInvalide) {
		t.Fatalf("attendu ErrCategorieInvalide (gibier sur entretien), obtenu %v", err)
	}

	_, err = store.Create(ctx, fieldlog.Entry{
		UserID:   "u5",
		Kind:     fieldlog.KindProbleme,
		Category: "cloture", // catégorie d'entretien, interdite en problème
	})
	if !errors.Is(err, fieldlog.ErrCategorieInvalide) {
		t.Fatalf("attendu ErrCategorieInvalide (cloture sur problème), obtenu %v", err)
	}
}

// TestSQLCheck_CroisementRejete : la base est la ligne de défense finale. Un INSERT
// SQL direct croisant kind='entretien' avec une catégorie de problème ('gibier'),
// qui contourne la validation Go de Store.Create, est rejeté par la CHECK croisée
// de la migration 00036.
func TestSQLCheck_CroisementRejete(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u7", "u7@example.com", 1)

	_, err := db.Exec(
		`INSERT INTO field_logs(id, user_id, kind, category) VALUES(?,?,?,?)`,
		"fl-cross", "u7", "entretien", "gibier",
	)
	if err == nil {
		t.Fatal("attendu un rejet de la CHECK croisée pour entretien×gibier, INSERT accepté")
	}

	// Le croisement symétrique (problème × catégorie d'entretien) est aussi rejeté.
	_, err = db.Exec(
		`INSERT INTO field_logs(id, user_id, kind, category) VALUES(?,?,?,?)`,
		"fl-cross2", "u7", "probleme", "cloture",
	)
	if err == nil {
		t.Fatal("attendu un rejet de la CHECK croisée pour probleme×cloture, INSERT accepté")
	}

	// Contre-épreuve : une combinaison cohérente passe.
	if _, err := db.Exec(
		`INSERT INTO field_logs(id, user_id, kind, category) VALUES(?,?,?,?)`,
		"fl-ok", "u7", "entretien", "cloture",
	); err != nil {
		t.Fatalf("combinaison cohérente entretien×cloture rejetée à tort: %v", err)
	}
}

// TestCreate_UserInactif : un membre inactif ne peut pas déclarer.
func TestCreate_UserInactif(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	mkUser(t, db, "u6", "u6@example.com", 0)
	store := &fieldlog.Store{DB: db}
	_, err := store.Create(context.Background(), fieldlog.Entry{
		UserID:   "u6",
		Kind:     fieldlog.KindEntretien,
		Category: "eau",
	})
	if !errors.Is(err, fieldlog.ErrUserInvalid) {
		t.Fatalf("attendu ErrUserInvalid, obtenu %v", err)
	}
}

// TestListForUser_IsolationCrossMembre : un membre ne voit que SES entrées, jamais
// celles d'un autre (garde IDOR V3a). La vue admin ListAll voit les deux.
func TestListForUser_IsolationCrossMembre(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	ctx := context.Background()
	mkUser(t, db, "uA", "ua@example.com", 1)
	mkUser(t, db, "uB", "ub@example.com", 1)
	store := &fieldlog.Store{DB: db}

	idA, err := store.Create(ctx, fieldlog.Entry{UserID: "uA", Kind: fieldlog.KindEntretien, Category: "eau"})
	if err != nil {
		t.Fatalf("Create uA: %v", err)
	}
	idB, err := store.Create(ctx, fieldlog.Entry{UserID: "uB", Kind: fieldlog.KindProbleme, Category: "divagation"})
	if err != nil {
		t.Fatalf("Create uB: %v", err)
	}

	listA, err := store.ListForUser(ctx, "uA")
	if err != nil {
		t.Fatalf("ListForUser uA: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != idA {
		t.Fatalf("uA: attendu uniquement %q, obtenu %+v", idA, listA)
	}
	if listA[0].ID == idB {
		t.Fatalf("uA: fuite cross-membre, entrée de uB visible")
	}

	listB, err := store.ListForUser(ctx, "uB")
	if err != nil {
		t.Fatalf("ListForUser uB: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != idB {
		t.Fatalf("uB: attendu uniquement %q, obtenu %+v", idB, listB)
	}

	all, err := store.ListAll(ctx, "")
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll: attendu 2 entrées, obtenu %d", len(all))
	}

	probs, err := store.ListAll(ctx, fieldlog.KindProbleme)
	if err != nil {
		t.Fatalf("ListAll(probleme): %v", err)
	}
	if len(probs) != 1 || probs[0].ID != idB {
		t.Fatalf("ListAll(probleme): attendu uniquement %q, obtenu %+v", idB, probs)
	}
}
