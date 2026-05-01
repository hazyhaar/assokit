package theme_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

func TestDefaults(t *testing.T) {
	d := theme.Defaults()
	if d.PrimaryColor == "" {
		t.Fatal("PrimaryColor vide")
	}
	if d.SiteName == "" {
		t.Fatal("SiteName vide")
	}
	if d.FontFamily == "" {
		t.Fatal("FontFamily vide")
	}
}

func TestCSSVars(t *testing.T) {
	d := theme.Defaults()
	css := d.CSSVars()
	for _, expected := range []string{
		":root {",
		"--color-primary:",
		"--color-bg:",
		"--color-text:",
		"--color-border:",
		"--font-body:",
		"--font-title:",
		"--radius:",
	} {
		if !strings.Contains(css, expected) {
			t.Errorf("CSSVars ne contient pas %q", expected)
		}
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE horui_config (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestLoadSaveRoundtrip(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	original := theme.Defaults()
	original.PrimaryColor = "#ff0000"
	original.SiteName = "Test Site"

	if err := theme.Save(db, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := theme.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.PrimaryColor != original.PrimaryColor {
		t.Errorf("PrimaryColor: got %q want %q", loaded.PrimaryColor, original.PrimaryColor)
	}
	if loaded.SiteName != original.SiteName {
		t.Errorf("SiteName: got %q want %q", loaded.SiteName, original.SiteName)
	}
}

func TestLoadEmptyDBReturnsDefaults(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	loaded, err := theme.Load(db)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	defaults := theme.Defaults()
	if loaded.PrimaryColor != defaults.PrimaryColor {
		t.Errorf("attendu defaults PrimaryColor %q, got %q", defaults.PrimaryColor, loaded.PrimaryColor)
	}
}
