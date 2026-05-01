package search_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/search"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
	"github.com/hazyhaar/assokit/internal/chassis"
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
	for _, r := range []struct{ id, label string }{
		{"admin", "Admin"}, {"moderator", "Mod"}, {"member", "Member"}, {"public", "Public"},
	} {
		db.Exec(`INSERT INTO roles(id,label) VALUES(?,?) ON CONFLICT DO NOTHING`, r.id, r.label)
	}
	return db
}

func TestQueryFindsNodes(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	e := &search.Engine{DB: db}
	ctx := context.Background()

	ts.Create(ctx, tree.Node{Title: "Liberté de la presse", Type: "page", BodyMD: "La presse libre est essentielle"})
	ts.Create(ctx, tree.Node{Title: "Santé publique", Type: "page", BodyMD: "Les lanceurs d'alerte protègent la santé"})

	hits, err := e.Query(ctx, "presse", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) == 0 {
		t.Error("attendu au moins 1 résultat pour 'presse'")
	}
}

func TestQueryFindsInBody(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	e := &search.Engine{DB: db}
	ctx := context.Background()

	ts.Create(ctx, tree.Node{Title: "Alerte", Type: "page", BodyMD: "whistleblower protection"})

	hits, err := e.Query(ctx, "whistleblower", 10)
	if err != nil {
		t.Fatalf("Query body: %v", err)
	}
	if len(hits) == 0 {
		t.Error("attendu un résultat sur le body")
	}
}

func TestQueryEmptyReturnsEmpty(t *testing.T) {
	db := newTestDB(t)
	e := &search.Engine{DB: db}
	ctx := context.Background()

	hits, err := e.Query(ctx, "", 10)
	if err != nil {
		t.Fatalf("Query empty: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("empty query should return 0 results, got %d", len(hits))
	}
}

func TestQueryFilteredRespectsPerms(t *testing.T) {
	db := newTestDB(t)
	ts := &tree.Store{DB: db}
	ps := &perms.Store{DB: db}
	e := &search.Engine{DB: db}
	ctx := context.Background()

	publicID, _ := ts.Create(ctx, tree.Node{Title: "Page publique alerte", Type: "page", BodyMD: "contenu public"})
	memberID, _ := ts.Create(ctx, tree.Node{Title: "Page membre alerte", Type: "page", BodyMD: "contenu membre"})

	ps.Set(ctx, publicID, "public", perms.PermRead)
	ps.Set(ctx, memberID, "member", perms.PermRead)

	// Public ne voit que la page publique
	hits, err := e.QueryFiltered(ctx, "alerte", []string{"public"}, 10)
	if err != nil {
		t.Fatalf("QueryFiltered public: %v", err)
	}
	for _, h := range hits {
		if h.NodeID == memberID {
			t.Error("public ne devrait pas voir la page membre")
		}
	}

	// Member voit les deux
	hits, err = e.QueryFiltered(ctx, "alerte", []string{"public", "member"}, 10)
	if err != nil {
		t.Fatalf("QueryFiltered member: %v", err)
	}
	foundPublic, foundMember := false, false
	for _, h := range hits {
		if h.NodeID == publicID {
			foundPublic = true
		}
		if h.NodeID == memberID {
			foundMember = true
		}
	}
	if !foundPublic || !foundMember {
		t.Errorf("member devrait voir les deux pages, public=%v member=%v", foundPublic, foundMember)
	}
}

func TestQueryMalformedNoError(t *testing.T) {
	db := newTestDB(t)
	e := &search.Engine{DB: db}
	ctx := context.Background()

	// Query avec opérateurs non fermés — ne doit pas paniquer ni retourner une erreur SQL
	_, err := e.Query(ctx, "AND OR (", 10)
	if err != nil {
		t.Errorf("malformed query should not return error, got %v", err)
	}
}
