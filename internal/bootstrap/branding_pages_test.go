package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	tree "github.com/hazyhaar/nodetree"

	_ "modernc.org/sqlite"
)

func openBrandingPagesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := tree.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func testBrandingFS(t *testing.T, files map[string]string) fs.FS {
	t.Helper()
	m := make(map[string]*fstest.MapFile, len(files))
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return fstest.MapFS(m)
}

func TestSeedBrandingPages_CreatesAndRenders(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	const sourceMD = "# About us\n\nWe are **here**."
	const wantBodyMD = "We are **here**."
	brandingFS := testBrandingFS(t, map[string]string{
		"pages/about.md": sourceMD,
	})

	if err := SeedBrandingPages(context.Background(), db, brandingFS, nil); err != nil {
		t.Fatalf("SeedBrandingPages: %v", err)
	}

	store := &tree.Store{DB: db}
	node, err := store.GetBySlug(context.Background(), "about")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if node.BodyMD != wantBodyMD {
		t.Errorf("BodyMD=%q want %q", node.BodyMD, wantBodyMD)
	}
	if node.BodyHTML == "" || node.BodyHTML == node.BodyMD {
		t.Errorf("BodyHTML should be rendered HTML, got %q", node.BodyHTML)
	}
	if !strings.Contains(node.BodyHTML, "<strong>here</strong>") {
		t.Errorf("BodyHTML missing bold render: %q", node.BodyHTML)
	}
	if node.Title != "About us" {
		t.Errorf("Title=%q want %q", node.Title, "About us")
	}
}

func TestSeedBrandingPages_AccueilMapsToHome(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	const sourceMD = "# Accueil AFP\n\nBienvenue."
	brandingFS := testBrandingFS(t, map[string]string{
		"pages/accueil.md": sourceMD,
	})

	if err := SeedBrandingPages(context.Background(), db, brandingFS, nil); err != nil {
		t.Fatalf("SeedBrandingPages: %v", err)
	}

	store := &tree.Store{DB: db}
	if _, err := store.GetBySlug(context.Background(), "home"); err != nil {
		t.Fatalf("accueil.md should seed slug home: %v", err)
	}
	if _, err := store.GetBySlug(context.Background(), "accueil"); !errors.Is(err, tree.ErrNotFound) {
		t.Fatalf("slug accueil should not exist, err=%v", err)
	}
}

func TestSeedBrandingPages_ReSeedIdenticalNoDuplicate(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	brandingFS := testBrandingFS(t, map[string]string{
		"pages/faq.md": "# FAQ\n\nQuestion?",
	})

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := SeedBrandingPages(ctx, db, brandingFS, nil); err != nil {
			t.Fatalf("SeedBrandingPages run %d: %v", i, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE slug='faq' AND type='page'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("want 1 page row, got %d", count)
	}
}

func TestSeedBrandingPages_MigratesLegacyFullMarkdownSeed(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	sourceMD := "# Page\n\nSeed content."
	wantBodyMD := "Seed content."
	brandingFS := testBrandingFS(t, map[string]string{
		"pages/info.md": sourceMD,
	})

	ctx := context.Background()
	store := &tree.Store{DB: db}
	if _, err := store.Create(ctx, tree.Node{
		Slug:   "info",
		Type:   "page",
		Title:  "Page",
		BodyMD: sourceMD,
	}); err != nil {
		t.Fatalf("Create legacy seed: %v", err)
	}

	if err := SeedBrandingPages(ctx, db, brandingFS, nil); err != nil {
		t.Fatalf("SeedBrandingPages migrate: %v", err)
	}

	got, err := store.GetBySlug(ctx, "info")
	if err != nil {
		t.Fatalf("GetBySlug after migrate: %v", err)
	}
	if got.BodyMD != wantBodyMD {
		t.Errorf("BodyMD=%q want migrated %q", got.BodyMD, wantBodyMD)
	}
}

func TestSeedBrandingPages_PreservesHumanEdit(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	seedMD := "# Page\n\nSeed content."
	humanMD := "# Page\n\nEdited by admin."
	brandingFS := testBrandingFS(t, map[string]string{
		"pages/info.md": seedMD,
	})

	ctx := context.Background()
	if err := SeedBrandingPages(ctx, db, brandingFS, nil); err != nil {
		t.Fatalf("initial seed: %v", err)
	}

	store := &tree.Store{DB: db}
	node, err := store.GetBySlug(ctx, "info")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	node.BodyMD = humanMD
	if err := store.Update(ctx, *node); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := SeedBrandingPages(ctx, db, brandingFS, nil); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	got, err := store.GetBySlug(ctx, "info")
	if err != nil {
		t.Fatalf("GetBySlug after re-seed: %v", err)
	}
	if got.BodyMD != humanMD {
		t.Errorf("BodyMD=%q want preserved human %q", got.BodyMD, humanMD)
	}
}

func TestSeedBrandingPages_NoPagesDirNoOp(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	brandingFS := testBrandingFS(t, map[string]string{
		"branding.toml": "name = \"Test\"\n",
	})

	if err := SeedBrandingPages(context.Background(), db, brandingFS, nil); err != nil {
		t.Fatalf("SeedBrandingPages: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("want 0 nodes, got %d", count)
	}
}

func TestSeedBrandingPages_NilFSNoOp(t *testing.T) {
	db := openBrandingPagesTestDB(t)
	defer db.Close()

	if err := SeedBrandingPages(context.Background(), db, nil, nil); err != nil {
		t.Fatalf("SeedBrandingPages: %v", err)
	}
}
