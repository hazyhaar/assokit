package theme

import (
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestInitAndBrand(t *testing.T) {
	// S'assurer qu'on part de 0 (pour l'ordre des tests)
	b := Brand()
	if b.Name != "" {
		t.Errorf("expected empty name, got %s", b.Name)
	}

	Init(&Branding{Name: "Test Asso", Texts: map[string]string{}})
	b = Brand()
	if b.Name != "Test Asso" {
		t.Errorf("expected 'Test Asso', got %s", b.Name)
	}
}

func TestT(t *testing.T) {
	Init(&Branding{
		Texts: map[string]string{
			"button.donate": "Faire un don",
			"empty.key":     "",
		},
	})

	if v := T("button.donate", "Fallback"); v != "Faire un don" {
		t.Errorf("expected 'Faire un don', got %s", v)
	}
	if v := T("missing.key", "Fallback"); v != "Fallback" {
		t.Errorf("expected 'Fallback', got %s", v)
	}
	if v := T("empty.key", "Fallback"); v != "Fallback" {
		t.Errorf("expected 'Fallback', got %s", v)
	}
}

func TestLoadFromFS_Valid(t *testing.T) {
	fsys := fstest.MapFS{
		"branding.toml": &fstest.MapFile{
			Data: []byte(`
name = "Mon Asso"
base_url = "https://example.org"
locale = "fr-FR"

[colors]
primary = "#1a73e8"

[[nav]]
slug = "/forum"
order = 2

[[nav]]
slug = "/participer"
order = 1
`),
		},
	}

	b, err := LoadFromFS(fsys, "branding.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Name != "Mon Asso" {
		t.Errorf("expected 'Mon Asso', got %s", b.Name)
	}
	if b.Colors.Primary != "#1a73e8" {
		t.Errorf("expected '#1a73e8', got %s", b.Colors.Primary)
	}
	if b.Colors.Secondary != "#34a853" { // Fallback
		t.Errorf("expected fallback '#34a853', got %s", b.Colors.Secondary)
	}
	if len(b.Nav) != 2 || b.Nav[0].Slug != "/participer" {
		t.Errorf("expected nav to be sorted")
	}
}

func TestLoadFromFS_Missing(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := LoadFromFS(fsys, "missing.toml")
	if err == nil {
		t.Errorf("expected error for missing file")
	}
}

func TestLoadFromFS_Validation(t *testing.T) {
	fsys := fstest.MapFS{
		"branding.toml": &fstest.MapFile{Data: []byte(`base_url = "https://example.org"`)}, // missing name
	}
	_, err := LoadFromFS(fsys, "branding.toml")
	if err == nil || !strings.Contains(err.Error(), "name cannot be empty") {
		t.Errorf("expected name validation error, got %v", err)
	}
}

func TestLoadFromFS_ColorsValidation(t *testing.T) {
	fsys := fstest.MapFS{
		"branding.toml": &fstest.MapFile{
			Data: []byte(`
name = "Test"
base_url = "https://test.com"
[colors]
primary = "#zzz"
secondary = "#abcdef"
`),
		},
	}
	b, err := LoadFromFS(fsys, "branding.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Colors.Primary != "#1a73e8" { // Fallback since invalid
		t.Errorf("expected fallback for primary, got %s", b.Colors.Primary)
	}
	if b.Colors.Secondary != "#abcdef" {
		t.Errorf("expected #abcdef for secondary, got %s", b.Colors.Secondary)
	}
}

func TestCSSVars(t *testing.T) {
	Init(&Branding{
		Colors: struct {
			Primary   string `toml:"primary"`
			Secondary string `toml:"secondary"`
			Accent    string `toml:"accent"`
			BgLight   string `toml:"bg_light"`
			BgDark    string `toml:"bg_dark"`
			Text      string `toml:"text"`
		}{
			Primary: "#000000",
		},
		FontFamily: "Inter",
	})
	css := CSSVars()
	if !strings.Contains(css, "--primary:#000000;") {
		t.Errorf("expected css to contain primary color, got %s", css)
	}
	if !strings.Contains(css, "--font:Inter;") {
		t.Errorf("expected css to contain font, got %s", css)
	}
}

func TestReloadFromFS_Race(t *testing.T) {
	Init(&Branding{Name: "Initial", Texts: map[string]string{}})

	fsys := fstest.MapFS{
		"branding.toml": &fstest.MapFile{
			Data: []byte(`name = "Reloaded"\nbase_url = "https://test.com"`),
		},
	}

	var wg sync.WaitGroup
	wg.Add(100)

	for i := 0; i < 50; i++ {
		go func() {
			defer wg.Done()
			_ = Brand().Name
			_ = T("key", "val")
		}()
	}

	go func() {
		_ = ReloadFromFS(fsys, "branding.toml")
	}()

	for i := 0; i < 50; i++ {
		go func() {
			defer wg.Done()
			_ = Brand().Name
			_ = T("key", "val")
		}()
	}

	wg.Wait()
}
