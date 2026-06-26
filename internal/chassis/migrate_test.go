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

// TestRunBackfillsLegacyGooseHistory : une DB déjà migrée par l'ancien runner
// goose (schema_version_goose) ne doit PAS ré-appliquer les migrations v3+ non
// idempotentes (ex : 00009 DROP TABLE signups). Le backfill reporte l'historique.
func TestRunBackfillsLegacyGooseHistory(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	// 1er Run : migre tout (fresh).
	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run initial: %v", err)
	}

	// Simule une DB issue de l'ancien double-runner : on déplace l'historique
	// v3+ de schema_version vers une table schema_version_goose legacy, puis on
	// purge ces versions de schema_version. Un Run naïf ré-appliquerait 00009.
	if _, err := db.Exec(`CREATE TABLE schema_version_goose (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TEXT
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version_goose(version_id, is_applied)
		SELECT version, 1 FROM schema_version WHERE version >= 3`); err != nil {
		t.Fatalf("seed legacy history: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version WHERE version >= 3`); err != nil {
		t.Fatalf("purge schema_version: %v", err)
	}

	// Insère une ligne signups : si 00009 (DROP TABLE signups) ré-exécute, elle disparaît.
	if _, err := db.Exec(`INSERT INTO signups(id, email, display_name, profile)
		VALUES('sig1', 'a@example.org', 'A', 'individuel')`); err != nil {
		t.Fatalf("insert signup: %v", err)
	}

	// 2e Run : le backfill doit empêcher toute ré-application.
	if err := chassis.Run(db); err != nil {
		t.Fatalf("Run after legacy: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM signups WHERE id='sig1'`).Scan(&n); err != nil {
		t.Fatalf("count signups: %v", err)
	}
	if n != 1 {
		t.Fatalf("signup perdu : 00009 a été ré-appliquée malgré le backfill (n=%d)", n)
	}
}
