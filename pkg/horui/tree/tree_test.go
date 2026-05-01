package tree_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/schema"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := schema.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestCreateRootAndChildren(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	rootID, err := s.Create(ctx, tree.Node{Title: "Root", Type: "folder"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	root, err := s.GetByID(ctx, rootID)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	if root.Depth != 0 {
		t.Errorf("root depth want 0 got %d", root.Depth)
	}

	childID, err := s.Create(ctx, tree.Node{
		Title:    "Child",
		Type:     "page",
		ParentID: sql.NullString{String: rootID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	child, err := s.GetByID(ctx, childID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.Depth != 1 {
		t.Errorf("child depth want 1 got %d", child.Depth)
	}

	grandChildID, err := s.Create(ctx, tree.Node{
		Title:    "GrandChild",
		Type:     "post",
		ParentID: sql.NullString{String: childID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	gc, _ := s.GetByID(ctx, grandChildID)
	if gc.Depth != 2 {
		t.Errorf("grandchild depth want 2 got %d", gc.Depth)
	}
}

func TestGetBySlugRoundtrip(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	id, _ := s.Create(ctx, tree.Node{Title: "Page Test", Type: "page", Slug: "page-test"})
	n, err := s.GetBySlug(ctx, "page-test")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if n.ID != id {
		t.Errorf("want %s got %s", id, n.ID)
	}
}

func TestUpdateBodyMDRegeneratesHTML(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	id, _ := s.Create(ctx, tree.Node{Title: "T", Type: "page", BodyMD: "initial"})
	n, _ := s.GetByID(ctx, id)
	n.BodyMD = "**bold text**"
	if err := s.Update(ctx, *n); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, _ := s.GetByID(ctx, id)
	if updated.BodyHTML == "" || updated.BodyHTML == "initial" {
		t.Errorf("body_html not regenerated: %q", updated.BodyHTML)
	}
	if !contains(updated.BodyHTML, "bold") {
		t.Errorf("body_html doesn't contain bold: %q", updated.BodyHTML)
	}
}

func TestSoftDeleteCascade(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	parentID, _ := s.Create(ctx, tree.Node{Title: "Parent", Type: "folder"})
	childID, _ := s.Create(ctx, tree.Node{
		Title:    "Child",
		Type:     "page",
		ParentID: sql.NullString{String: parentID, Valid: true},
	})

	if err := s.SoftDelete(ctx, parentID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	// Parent introuvable
	if _, err := s.GetByID(ctx, parentID); !errors.Is(err, tree.ErrNotFound) {
		t.Errorf("parent should be NotFound, got %v", err)
	}
	// Child aussi soft-deleted
	if _, err := s.GetByID(ctx, childID); !errors.Is(err, tree.ErrNotFound) {
		t.Errorf("child should be NotFound, got %v", err)
	}
}

func TestSlugCollision(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	s.Create(ctx, tree.Node{Title: "Page", Slug: "mon-slug", Type: "page"})
	_, err := s.Create(ctx, tree.Node{Title: "Page2", Slug: "mon-slug", Type: "page"})
	if !errors.Is(err, tree.ErrSlugTaken) {
		t.Errorf("want ErrSlugTaken, got %v", err)
	}
}

func TestCyclePrevention(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	rootID, _ := s.Create(ctx, tree.Node{Title: "Root", Type: "folder"})
	childID, _ := s.Create(ctx, tree.Node{
		Title:    "Child",
		Type:     "folder",
		ParentID: sql.NullString{String: rootID, Valid: true},
	})

	// Reparenter root sous child = cycle
	if err := s.CheckCycle(ctx, rootID, childID); !errors.Is(err, tree.ErrCycle) {
		t.Errorf("want ErrCycle, got %v", err)
	}

	// Nœud comme son propre parent = cycle
	if err := s.CheckCycle(ctx, rootID, rootID); !errors.Is(err, tree.ErrCycle) {
		t.Errorf("want ErrCycle (self), got %v", err)
	}
}

func TestChildren(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	parentID, _ := s.Create(ctx, tree.Node{Title: "Parent", Type: "folder"})
	s.Create(ctx, tree.Node{Title: "C1", Type: "page", ParentID: sql.NullString{String: parentID, Valid: true}})
	s.Create(ctx, tree.Node{Title: "C2", Type: "page", ParentID: sql.NullString{String: parentID, Valid: true}})

	children, err := s.Children(ctx, parentID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("want 2 children, got %d", len(children))
	}
}

func TestRoots(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	s.Create(ctx, tree.Node{Title: "R1", Type: "folder"})
	s.Create(ctx, tree.Node{Title: "R2", Type: "folder"})

	roots, err := s.Roots(ctx)
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 2 {
		t.Errorf("want 2 roots, got %d", len(roots))
	}
}

func TestAncestors(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	r := makeNode(t, s, "Root")
	c := makeNodeUnder(t, s, "Child", r)
	gc := makeNodeUnder(t, s, "GrandChild", c)

	ancs, err := s.Ancestors(ctx, gc)
	if err != nil {
		t.Fatalf("Ancestors: %v", err)
	}
	if len(ancs) != 2 {
		t.Errorf("want 2 ancestors, got %d", len(ancs))
	}
}

func TestSubtree(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	r := makeNode(t, s, "Root")
	makeNodeUnder(t, s, "Child1", r)
	c2 := makeNodeUnder(t, s, "Child2", r)
	makeNodeUnder(t, s, "GrandChild", c2)

	nodes, err := s.Subtree(ctx, r, 2)
	if err != nil {
		t.Fatalf("Subtree: %v", err)
	}
	if len(nodes) != 4 {
		t.Errorf("want 4 nodes in subtree, got %d", len(nodes))
	}
}

func TestReorder(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	parent := makeNode(t, s, "Parent")
	c1 := makeNodeUnder(t, s, "C1", parent)
	c2 := makeNodeUnder(t, s, "C2", parent)

	if err := s.Reorder(ctx, parent, []string{c2, c1}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
}

func TestSlugAutoGenerated(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	id, err := s.Create(ctx, tree.Node{Title: "Ma Page Test", Type: "page"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, _ := s.GetByID(ctx, id)
	if n.Slug == "" {
		t.Error("slug auto-généré ne doit pas être vide")
	}
}

func TestGetByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	s := &tree.Store{DB: db}
	ctx := context.Background()

	_, err := s.GetByID(ctx, "inexistant")
	if !errors.Is(err, tree.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func makeNode(t *testing.T, s *tree.Store, title string) string {
	t.Helper()
	id, err := s.Create(context.Background(), tree.Node{Title: title, Type: "folder"})
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return id
}

func makeNodeUnder(t *testing.T, s *tree.Store, title, parentID string) string {
	t.Helper()
	id, err := s.Create(context.Background(), tree.Node{
		Title:    title,
		Type:     "page",
		ParentID: sql.NullString{String: parentID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create %s under %s: %v", title, parentID, err)
	}
	return id
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
