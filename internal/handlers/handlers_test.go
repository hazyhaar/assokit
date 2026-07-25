package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
	tree "github.com/hazyhaar/nodetree"
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
	return db
}

func seedRoles(t *testing.T, db *sql.DB) {
	t.Helper()
	// RBAC migré : les rôles sont des grades (grades.name). Seed des grades système.
	for _, g := range []struct{ id, name string }{
		{"sys-admin", "admin"}, {"sys-moderator", "moderator"},
		{"sys-member", "member"}, {"sys-guest", "guest"},
	} {
		if _, err := db.Exec(`INSERT INTO grades(id,name,system) VALUES(?,?,1) ON CONFLICT DO NOTHING`, g.id, g.name); err != nil {
			t.Fatalf("seed grade %s: %v", g.id, err)
		}
	}
}

// TestSignup_CreateMember_TxRollbackOnError vérifie que la TX est rollback
// complète quand l'INSERT user échoue. On déclenche l'erreur via un email en
// doublon (UNIQUE violation sur users) : le premier createMember réussit, le
// second doit échouer ET ne laisser aucune ligne signup orpheline.
func TestSignup_CreateMember_TxRollbackOnError(t *testing.T) {
	db := newTestDB(t)
	seedRoles(t, db)
	ctx := context.Background()

	// Premier membre : succès.
	if _, err := createMember(ctx, db, "dup@example.com", "First", signupprofile.Profile{ID: "adherent"}, "{}", "127.0.0.1", []byte("secret")); err != nil {
		t.Fatalf("premier createMember devrait réussir : %v", err)
	}

	var signupsBefore int
	db.QueryRow(`SELECT COUNT(*) FROM signups`).Scan(&signupsBefore)

	// Second membre, même email : UNIQUE violation sur users → la TX entière rollback.
	_, err := createMember(ctx, db, "dup@example.com", "Second", signupprofile.Profile{ID: "adherent"}, "{}", "127.0.0.1", []byte("secret"))
	if err == nil {
		t.Fatal("second createMember devrait échouer (email dupliqué)")
	}

	var userCount int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount)
	if userCount != 1 {
		t.Errorf("TX rollback attendu : 1 seul user, got %d", userCount)
	}

	// La ligne signup du second appel ne doit pas avoir survécu au rollback.
	var signupsAfter int
	db.QueryRow(`SELECT COUNT(*) FROM signups`).Scan(&signupsAfter)
	if signupsAfter != signupsBefore {
		t.Errorf("TX rollback attendu : signups inchangé (%d), got %d", signupsBefore, signupsAfter)
	}
}

// TestForum_Reply_RejectsAtMaxDepth vérifie que handleForumReply retourne 400
// quand le parent est à profondeur ForumMaxDepth-1 (profondeur max atteinte).
func TestForum_Reply_RejectsAtMaxDepth(t *testing.T) {
	db := newTestDB(t)
	seedRoles(t, db)

	treeStore := &tree.Store{DB: db}
	ctx := context.Background()

	// Créer un nœud à depth ForumMaxDepth-1 = 2
	// D'abord créer les parents nécessaires pour respecter la contrainte de depth
	rootID, err := treeStore.Create(ctx, tree.Node{Slug: "root-test", Type: "folder", Title: "Root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	lvl1ID, err := treeStore.Create(ctx, tree.Node{
		Slug: "lvl1-test", Type: "post", Title: "Level 1",
		ParentID: newNullString(rootID),
	})
	if err != nil {
		t.Fatalf("create lvl1: %v", err)
	}
	lvl2ID, err := treeStore.Create(ctx, tree.Node{
		Slug: "lvl2-test", Type: "post", Title: "Level 2",
		ParentID: newNullString(lvl1ID),
	})
	if err != nil {
		t.Fatalf("create lvl2: %v", err)
	}
	lvl3ID, err := treeStore.Create(ctx, tree.Node{
		Slug: "lvl3-test", Type: "post", Title: "Level 3",
		ParentID: newNullString(lvl2ID),
	})
	if err != nil {
		t.Fatalf("create lvl3: %v", err)
	}
	lvl4ID, err := treeStore.Create(ctx, tree.Node{
		Slug: "lvl4-test", Type: "post", Title: "Level 4",
		ParentID: newNullString(lvl3ID),
	})
	if err != nil {
		t.Fatalf("create lvl4: %v", err)
	}
	_ = lvl4ID

	// Vérifier que depth = ForumMaxDepth-1 (la station ajoute le niveau branche :
	// racine 0 -> catégorie 1 -> question 2 -> branche 3 -> message 4, plafond de réponse à 4).
	parentNode, err := treeStore.GetBySlug(ctx, "lvl4-test")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if parentNode.Depth != ForumMaxDepth-1 {
		t.Fatalf("depth attendu %d got %d", ForumMaxDepth-1, parentNode.Depth)
	}

	deps := app.AppDeps{DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := handleForumReply(deps)

	// Injecter l'utilisateur et le paramètre chi {slug}
	user := &identity.User{ID: "user-test", Email: "u@test.com", Roles: []string{"member"}}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "lvl4-test")

	r := httptest.NewRequest(http.MethodPost, "/forum/lvl4-test/reply", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("profondeur max : attendu 400, got %d", w.Code)
	}
}
