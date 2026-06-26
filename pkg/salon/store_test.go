package salon_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// openDB ouvre une DB SQLite en mémoire avec FK activées et les migrations appliquées.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// insertUser insère un utilisateur minimal dans la table users.
func insertUser(t *testing.T, db *sql.DB, id, email string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users(id,email,password_hash,display_name,is_active) VALUES(?,?,?,?,1)`,
		id, email, "x", email,
	); err != nil {
		t.Fatalf("insert user %s: %v", id, err)
	}
}

func TestSalon_Create(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "Mon Premier Salon", "alice", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sl.Slug == "" {
		t.Fatal("slug vide")
	}
	if sl.ID == "" {
		t.Fatal("id vide")
	}
	if sl.Name != "Mon Premier Salon" {
		t.Fatalf("name inattendu: %q", sl.Name)
	}

	// Vérifier que alice est bien membre role='owner'.
	var role string
	if err := db.QueryRow(`SELECT role FROM salon_member WHERE salon_id=? AND user_id=?`, sl.ID, "alice").Scan(&role); err != nil {
		t.Fatalf("salon_member: %v", err)
	}
	if role != "owner" {
		t.Fatalf("rôle attendu 'owner', obtenu %q", role)
	}
}

func TestSalon_SlugCollision_Unique(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl1, err := store.Create(ctx, "Général", "alice", "", false)
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	sl2, err := store.Create(ctx, "Général", "alice", "", false)
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if sl1.Slug == sl2.Slug {
		t.Fatalf("collision slug: %q", sl1.Slug)
	}
}

func TestSalon_Join_PasswordOK(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")
	insertUser(t, db, "bob", "bob@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "Privé", "alice", "secret123", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Bon mot de passe → succès.
	if err := store.Join(ctx, sl.Slug, "bob", "secret123"); err != nil {
		t.Fatalf("Join mot de passe correct: %v", err)
	}
}

func TestSalon_Join_PasswordKO(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")
	insertUser(t, db, "bob", "bob@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "Privé", "alice", "secret123", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mauvais mot de passe → ErrWrongPassword.
	err = store.Join(ctx, sl.Slug, "bob", "mauvais")
	if err != salon.ErrWrongPassword {
		t.Fatalf("attendu ErrWrongPassword, obtenu: %v", err)
	}
}

func TestSalon_PostMessage_BodyHTMLEscaped(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "Tech", "alice", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Injection XSS tentative.
	body := `<script>alert("xss")</script>`
	id, err := store.PostMessage(ctx, sl.Slug, "alice", body)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	var bodyHTML string
	if err := db.QueryRow(`SELECT body_html FROM salon_message WHERE id=?`, id).Scan(&bodyHTML); err != nil {
		t.Fatalf("select body_html: %v", err)
	}
	// goldmark sans WithUnsafe doit échapper la balise script brute.
	if strings.Contains(bodyHTML, "<script>") {
		t.Fatalf("body_html contient <script> brut (XSS non échappé): %q", bodyHTML)
	}
}

func TestSalon_Archive(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "alice", "alice@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "À archiver", "alice", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Archive(ctx, sl.Slug); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// Vérifier archived_at non NULL.
	var archivedAt sql.NullString
	if err := db.QueryRow(`SELECT archived_at FROM salon WHERE id=?`, sl.ID).Scan(&archivedAt); err != nil {
		t.Fatalf("select archived_at: %v", err)
	}
	if !archivedAt.Valid {
		t.Fatal("archived_at NULL après Archive()")
	}

	// PostMessage sur salon archivé doit échouer.
	_, err = store.PostMessage(ctx, sl.Slug, "alice", "test")
	if err != salon.ErrArchived {
		t.Fatalf("attendu ErrArchived après archive, obtenu: %v", err)
	}
}

func TestSalon_TranscriptionConsent(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "bob", "bob@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	sl, err := store.Create(ctx, "Salon Transcription", "bob", "", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Par défaut : consentement désactivé, même si visio active.
	ok, err := store.ShouldTranscribe(ctx, sl.Slug)
	if err != nil {
		t.Fatalf("ShouldTranscribe (défaut): %v", err)
	}
	if ok {
		t.Fatal("ShouldTranscribe doit être false sans consentement")
	}

	// Activer le consentement.
	if err := store.ToggleTranscriptionConsent(ctx, sl.Slug, true); err != nil {
		t.Fatalf("ToggleTranscriptionConsent on: %v", err)
	}

	// Visio active + consent active → true.
	ok, err = store.ShouldTranscribe(ctx, sl.Slug)
	if err != nil {
		t.Fatalf("ShouldTranscribe (consent on): %v", err)
	}
	if !ok {
		t.Fatal("ShouldTranscribe doit être true avec visio_enabled + consent")
	}

	// Vérifier que Get retourne bien le flag persisté.
	loaded, err := store.Get(ctx, sl.Slug)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !loaded.TranscriptionConsent {
		t.Fatal("TranscriptionConsent non persisté après ToggleTranscriptionConsent(true)")
	}

	// Désactiver le consentement → false même si visio toujours active.
	if err := store.ToggleTranscriptionConsent(ctx, sl.Slug, false); err != nil {
		t.Fatalf("ToggleTranscriptionConsent off: %v", err)
	}
	ok, err = store.ShouldTranscribe(ctx, sl.Slug)
	if err != nil {
		t.Fatalf("ShouldTranscribe (consent off): %v", err)
	}
	if ok {
		t.Fatal("ShouldTranscribe doit être false après désactivation du consentement")
	}
}

func TestSalon_ShouldTranscribe_VisioOff(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	insertUser(t, db, "carol", "carol@example.org")

	store := &salon.Store{DB: db}
	ctx := context.Background()

	// Salon sans visio.
	sl, err := store.Create(ctx, "Salon Sans Visio", "carol", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Activer le consentement malgré visio désactivée.
	if err := store.ToggleTranscriptionConsent(ctx, sl.Slug, true); err != nil {
		t.Fatalf("ToggleTranscriptionConsent: %v", err)
	}

	// ShouldTranscribe doit rester false car visio désactivée.
	ok, err := store.ShouldTranscribe(ctx, sl.Slug)
	if err != nil {
		t.Fatalf("ShouldTranscribe: %v", err)
	}
	if ok {
		t.Fatal("ShouldTranscribe doit être false si visio désactivée, même avec consent")
	}
}
