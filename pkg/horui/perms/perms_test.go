package perms_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
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
	// Seed roles
	for _, r := range []struct{ id, label string }{
		{"admin", "Admin"}, {"moderator", "Mod"}, {"member", "Member"}, {"public", "Public"},
	} {
		db.Exec(`INSERT INTO roles(id,label) VALUES(?,?) ON CONFLICT DO NOTHING`, r.id, r.label)
	}
	return db
}

func makeNode(t *testing.T, s *tree.Store, title string, parentID string) string {
	t.Helper()
	n := tree.Node{Title: title, Type: "page"}
	if parentID != "" {
		n.ParentID = sql.NullString{String: parentID, Valid: true}
	}
	id, err := s.Create(context.Background(), n)
	if err != nil {
		t.Fatalf("create node %s: %v", title, err)
	}
	return id
}

func TestSetGetRoundtrip(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	nodeID := makeNode(t, ts, "Page", "")

	if err := ps.Set(ctx, nodeID, "public", perms.PermRead); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p, err := ps.Get(ctx, nodeID, "public")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p != perms.PermRead {
		t.Errorf("want PermRead got %s", p)
	}
}

func TestUnset(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	nodeID := makeNode(t, ts, "Page", "")
	ps.Set(ctx, nodeID, "public", perms.PermRead)
	ps.Unset(ctx, nodeID, "public")

	p, _ := ps.Get(ctx, nodeID, "public")
	if p != perms.PermNone {
		t.Errorf("after Unset want PermNone got %s", p)
	}
}

func TestEffectiveInheritance(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	rootID := makeNode(t, ts, "Root", "")
	childID := makeNode(t, ts, "Child", rootID)

	// Pose read sur la racine
	ps.Set(ctx, rootID, "public", perms.PermRead)

	// L'enfant hérite
	p, err := ps.Effective(ctx, childID, "public")
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if p != perms.PermRead {
		t.Errorf("child should inherit PermRead, got %s", p)
	}
}

func TestEffectiveOverride(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	rootID := makeNode(t, ts, "Root", "")
	childID := makeNode(t, ts, "Child", rootID)

	ps.Set(ctx, rootID, "public", perms.PermWrite)
	ps.Set(ctx, childID, "public", perms.PermNone) // override restrictif

	p, _ := ps.Effective(ctx, childID, "public")
	if p != perms.PermNone {
		t.Errorf("child override should be PermNone, got %s", p)
	}
}

func TestUserCanMultiRoles(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	nodeID := makeNode(t, ts, "Forum", "")
	ps.Set(ctx, nodeID, "public", perms.PermRead)
	ps.Set(ctx, nodeID, "member", perms.PermWrite)

	// User avec roles public+member : peut écrire
	can, err := ps.UserCan(ctx, []string{"public", "member"}, nodeID, perms.PermWrite)
	if err != nil {
		t.Fatalf("UserCan: %v", err)
	}
	if !can {
		t.Error("user with member role should be able to write")
	}

	// User avec juste public : ne peut pas écrire
	can, _ = ps.UserCan(ctx, []string{"public"}, nodeID, perms.PermWrite)
	if can {
		t.Error("public-only user should not be able to write")
	}
}

func TestNodesUserCanRead(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	ctx := context.Background()

	n1 := makeNode(t, ts, "Public Page", "")
	n2 := makeNode(t, ts, "Member Page", "")
	n3 := makeNode(t, ts, "Private Page", "")

	ps.Set(ctx, n1, "public", perms.PermRead)
	ps.Set(ctx, n2, "member", perms.PermRead)
	ps.Set(ctx, n3, "admin", perms.PermAdmin)

	// Public : voit n1
	ids, err := ps.NodesUserCanRead(ctx, []string{"public"})
	if err != nil {
		t.Fatalf("NodesUserCanRead: %v", err)
	}
	if !containsID(ids, n1) || containsID(ids, n2) {
		t.Errorf("public should see n1 only, got %v", ids)
	}

	// Member : voit n1 et n2
	ids, _ = ps.NodesUserCanRead(ctx, []string{"public", "member"})
	if !containsID(ids, n1) || !containsID(ids, n2) {
		t.Errorf("member should see n1+n2, got %v", ids)
	}
	_ = n3
}

func containsID(ids []string, id string) bool {
	for _, i := range ids {
		if i == id {
			return true
		}
	}
	return false
}
