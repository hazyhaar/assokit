package setup_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/setup"
)

func byKey(items []setup.Item, key string) setup.Item {
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	return setup.Item{}
}

func TestStatus_EmptyConfig(t *testing.T) {
	db, _ := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	deps := app.AppDeps{DB: db, Config: config.Config{}}

	items := setup.Status(deps)
	// Config vide : base_url, email, connectors, admin tous manquants.
	for _, key := range []string{"base_url", "email", "connectors_helloasso", "admin_account"} {
		if got := byKey(items, key).State; got != setup.StateMissing {
			t.Errorf("%s: state=%s, want missing", key, got)
		}
	}
	// Sessions hors prod = partiel (secret aléatoire toléré).
	if got := byKey(items, "session_security").State; got != setup.StatePartial {
		t.Errorf("session_security: state=%s, want partial", got)
	}
	// connectors expose un lien d'édition DB-safe.
	if byKey(items, "connectors_helloasso").EditURL != "/admin/connectors" {
		t.Errorf("connectors EditURL manquant")
	}
}

func TestStatus_Configured(t *testing.T) {
	db, _ := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatal(err)
	}
	// Un admin existe.
	if _, err := db.Exec(`INSERT OR IGNORE INTO grades(id,name,system) VALUES('sys-admin','admin',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES('a','a@example.org','x','A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_grades(user_id, grade_id) VALUES('a','sys-admin')`); err != nil {
		t.Fatal(err)
	}
	deps := app.AppDeps{DB: db, Config: config.Config{
		BaseURL:           "https://c.example.org",
		Prod:              true,
		MailerConfigured:  true,
		ConnectorsEnabled: true,
		GoogleClientID:    "gid",
	}}

	items := setup.Status(deps)
	for _, key := range []string{"base_url", "session_security", "email", "connectors_helloasso", "social_login_google", "admin_account"} {
		if got := byKey(items, key).State; got != setup.StateOK {
			t.Errorf("%s: state=%s, want ok", key, got)
		}
	}
}
