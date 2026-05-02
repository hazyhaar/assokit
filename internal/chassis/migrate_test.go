package chassis_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func TestRunAppliesV1(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run v1: %v", err)
	}

	// Vérifie que les tables principales existent
	for _, table := range []string{"nodes", "users", "roles", "signups", "email_outbox", "activation_tokens", "horui_config", "schema_version"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q absente après migration v1: %v", table, err)
		}
	}

	// Vérifie schema_version
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_version WHERE version=1`).Scan(&version); err != nil {
		t.Fatalf("schema_version v1 absente: %v", err)
	}
}

func TestRunIdempotent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	// Deuxième appel doit être un no-op sans erreur
	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run 2 (idempotent): %v", err)
	}
}

// TestMigrateV2_FeedbacksTableExists : table feedbacks présente après Run.
func TestMigrateV2_FeedbacksTableExists(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='feedbacks'`).Scan(&name); err != nil {
		t.Fatalf("table feedbacks absente après migration v2: %v", err)
	}
	if name != "feedbacks" {
		t.Errorf("name = %q, want feedbacks", name)
	}

	var v int
	if err := db.QueryRow(`SELECT version FROM schema_version WHERE version=2`).Scan(&v); err != nil {
		t.Fatalf("schema_version v2 absente: %v", err)
	}
}

// TestMigrateV2_FeedbackInsertRespectsCheckLength : CHECK length(message) BETWEEN 5 AND 2000.
func TestMigrateV2_FeedbackInsertRespectsCheckLength(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// message 4 chars → doit échouer.
	_, err := db.Exec(`INSERT INTO feedbacks(id, page_url, message) VALUES('fb-fail','/test','abcd')`)
	if err == nil {
		t.Error("INSERT message 4 chars devrait échouer (CHECK length >= 5)")
	}

	// message 5 chars → doit passer.
	_, err = db.Exec(`INSERT INTO feedbacks(id, page_url, message) VALUES('fb-ok','/test','abcde')`)
	if err != nil {
		t.Errorf("INSERT message 5 chars devrait passer: %v", err)
	}
}

// TestMigrateV2_FeedbackInsertRespectsCheckStatus : CHECK status IN ('pending','triaged','closed','spam').
func TestMigrateV2_FeedbackInsertRespectsCheckStatus(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// status invalide → doit échouer.
	_, err := db.Exec(`INSERT INTO feedbacks(id, page_url, message, status) VALUES('fb-bad','/test','message ok','invalid')`)
	if err == nil {
		t.Error("INSERT status='invalid' devrait échouer (CHECK status IN ...)")
	}

	// status valide → doit passer.
	_, err = db.Exec(`INSERT INTO feedbacks(id, page_url, message, status) VALUES('fb-good','/test','message ok','spam')`)
	if err != nil {
		t.Errorf("INSERT status='spam' devrait passer: %v", err)
	}
}

// TestMigrateV2_Idempotent : Run() 2x sans erreur (v2 no-op au 2e appel).
func TestMigrateV2_Idempotent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run 2 (idempotent v2): %v", err)
	}

	// La table ne doit pas être dupliquée.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feedbacks'`).Scan(&count); err != nil {
		t.Fatalf("COUNT feedbacks: %v", err)
	}
	if count != 1 {
		t.Errorf("feedbacks table count = %d after 2x Run, want 1", count)
	}
}

func TestFTS5TriggersWork(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Insérer un rôle + user pour satisfaire les FK
	db.Exec(`INSERT INTO roles VALUES ('public','Public')`)
	db.Exec(`INSERT INTO users(id, email, password_hash, display_name) VALUES ('u1','a@b.com','x','Test')`)

	// Insérer un nœud → trigger FTS insert
	_, err := db.Exec(`INSERT INTO nodes(id, slug, type, title, body_md) VALUES ('n1','test-slug','page','Titre test','Corps du test')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// En mode external content (content='nodes'), il faut joindre sur rowid pour récupérer les colonnes.
	var id string
	err = db.QueryRow(`
		SELECT n.id FROM nodes n
		JOIN (SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH 'Corps') fts ON n.rowid = fts.rowid
	`).Scan(&id)
	if err != nil {
		t.Fatalf("FTS search: %v", err)
	}
	if id != "n1" {
		t.Errorf("FTS returned %q, want n1", id)
	}

	// Update → trigger delete+insert
	db.Exec(`UPDATE nodes SET title='Titre modifié' WHERE id='n1'`)
	err = db.QueryRow(`
		SELECT n.id FROM nodes n
		JOIN (SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH 'modifié') fts ON n.rowid = fts.rowid
	`).Scan(&id)
	if err != nil {
		t.Fatalf("FTS search après update: %v", err)
	}
	if id != "n1" {
		t.Errorf("FTS après update returned %q, want n1", id)
	}
}
