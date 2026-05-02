// CLAUDE:SUMMARY Tests gardiens forum — snippet/stripTags/BuildThread coverage 0%→minimum (M-ASSOKIT-AUDIT-FIX-2).
package forum

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

func openForumTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY, parent_id TEXT, slug TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK(type IN ('folder','page','post','form','doc')),
			title TEXT NOT NULL, body_md TEXT NOT NULL DEFAULT '', body_html TEXT NOT NULL DEFAULT '',
			visibility TEXT NOT NULL DEFAULT 'public',
			author_id TEXT, display_order INTEGER NOT NULL DEFAULT 0, depth INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TEXT
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestForum_SnippetTruncation_WordBoundary : snippet tronque à mot complet, ajoute …
func TestForum_SnippetTruncation_WordBoundary(t *testing.T) {
	long := strings.Repeat("hello world ", 30) // 360 chars
	got := snippet(long, 50)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("snippet long n'ajoute pas '…' : %q", got)
	}
	if strings.Contains(got, "hellworld") {
		t.Errorf("snippet coupe mid-word : %q", got)
	}
	if len(got) > 60 {
		t.Errorf("snippet trop long : len=%d, max~50", len(got))
	}
}

// TestForum_SnippetEdgeCases : empty, short, exact-max.
func TestForum_SnippetEdgeCases(t *testing.T) {
	if got := snippet("", 50); got != "" {
		t.Errorf("snippet(\"\") = %q, attendu \"\"", got)
	}
	if got := snippet("court", 50); got != "court" {
		t.Errorf("snippet court tronqué : %q", got)
	}
	exact := strings.Repeat("a", 50)
	if got := snippet(exact, 50); got != exact {
		t.Errorf("snippet exact-max altéré : got %q", got)
	}
}

// TestForum_StripTagsMalformedHTML : tags non fermés, nested.
func TestForum_StripTagsMalformedHTML(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<p>hello</p>", "hello"},
		{"<p>hello", "hello"},                          // tag ouvert non fermé : tout reste après '>' présumé
		{"<a href=\"x\">link<b>bold</b></a>", "linkbold"},
		{"<<doublé>>texte", "texte"},
		{"sans tags", "sans tags"},
		{"", ""},
	}
	for _, c := range cases {
		got := stripTags(c.in)
		if got != c.want {
			t.Errorf("stripTags(%q) = %q, attendu %q", c.in, got, c.want)
		}
	}
}

// TestForum_BuildThread_MaxLoadDepthRespected : tree 5 niveaux, maxDepth=2 → 2 niveaux.
func TestForum_BuildThread_MaxLoadDepthRespected(t *testing.T) {
	db := openForumTestDB(t)
	store := &tree.Store{DB: db}
	ctx := context.Background()

	// Créer arbre 5 niveaux
	rootID, err := store.Create(ctx, tree.Node{Slug: "n0", Type: "folder", Title: "root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	parentID := rootID
	for i := 1; i <= 5; i++ {
		n := tree.Node{
			Slug:     "n" + string(rune('0'+i)),
			Type:     "post",
			Title:    "level " + string(rune('0'+i)),
			ParentID: sql.NullString{String: parentID, Valid: true},
		}
		id, err := store.Create(ctx, n)
		if err != nil {
			t.Fatalf("create level %d: %v", i, err)
		}
		parentID = id
	}

	rootNode, err := store.GetBySlug(ctx, "n0")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}

	// BuildThread maxDepth=2 → root + 1 enfant + 0 grandchild loaded mais ChildCount=1
	tn, err := BuildThread(ctx, store, *rootNode, nil, 2)
	if err != nil {
		t.Fatalf("BuildThread: %v", err)
	}
	if len(tn.Children) != 1 {
		t.Fatalf("level 1: %d children, want 1", len(tn.Children))
	}
	if len(tn.Children[0].Children) != 1 {
		t.Fatalf("level 2: %d children, want 1", len(tn.Children[0].Children))
	}
	// Niveau 3 : Children doit être vide ou nil (au-delà de maxDepth)
	if len(tn.Children[0].Children[0].Children) != 0 {
		t.Errorf("level 3: %d children, want 0 (au-delà de maxDepth)", len(tn.Children[0].Children[0].Children))
	}
	// Mais ChildCount doit refléter qu'il y a bien des enfants en DB
	if tn.Children[0].Children[0].ChildCount == 0 {
		t.Error("level 3 ChildCount=0 alors qu'il existe des enfants en DB")
	}
}

// TestForum_RepliesLabel : pluriel français.
func TestForum_RepliesLabel(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "réponses"}, {1, "réponse"}, {2, "réponses"}, {10, "réponses"},
	}
	for _, c := range cases {
		if got := repliesLabel(c.n); got != c.want {
			t.Errorf("repliesLabel(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestForum_Truncate_EdgeCases : truncate helper.
func TestForum_Truncate_EdgeCases(t *testing.T) {
	if got := truncate("hello world", 5); !strings.HasSuffix(got, "…") {
		t.Errorf("truncate hello world to 5 = %q, attendu suffix '…'", got)
	}
	if got := truncate("ok", 10); got != "ok" {
		t.Errorf("truncate ok to 10 = %q, attendu 'ok'", got)
	}
}
