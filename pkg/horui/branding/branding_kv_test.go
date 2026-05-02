package branding_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/branding"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

func TestBrandingKV_SetAndGet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	if err := branding.Set(db, "identite.nom_asso", "Nonpossumus", "text", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := branding.Get(db, "identite.nom_asso")
	if got != "Nonpossumus" {
		t.Errorf("Get: attendu %q, got %q", "Nonpossumus", got)
	}
}

func TestBrandingKV_GetMissing(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	got := branding.Get(db, "key.inexistante")
	if got != "" {
		t.Errorf("GetMissing: attendu \"\", got %q", got)
	}
}

func TestBrandingKV_Progress(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	// 3 champs : 2 required, 1 optional
	fields := []branding.FieldDef{
		{Key: "f.required1", Required: true},
		{Key: "f.required2", Required: true},
		{Key: "f.optional1", Required: false},
	}

	// Remplir seulement le premier required
	if err := branding.Set(db, "f.required1", "valeur", "text", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	info := branding.GetProgress(db, fields)

	if info.RequiredTotal != 2 {
		t.Errorf("RequiredTotal: attendu 2, got %d", info.RequiredTotal)
	}
	if info.RequiredFilled != 1 {
		t.Errorf("RequiredFilled: attendu 1, got %d", info.RequiredFilled)
	}
	if info.AllTotal != 3 {
		t.Errorf("AllTotal: attendu 3, got %d", info.AllTotal)
	}
	if info.AllFilled != 1 {
		t.Errorf("AllFilled: attendu 1, got %d", info.AllFilled)
	}
}
