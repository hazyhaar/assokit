package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
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
	if err := schema.Run(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func seedRoles(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, r := range []struct{ id, label string }{
		{"admin", "Administrateur"}, {"moderator", "Modérateur"},
		{"member", "Membre"}, {"public", "Public"},
	} {
		if _, err := db.Exec(`INSERT INTO roles(id,label) VALUES(?,?) ON CONFLICT DO NOTHING`, r.id, r.label); err != nil {
			t.Fatalf("seed role %s: %v", r.id, err)
		}
	}
}

// TestSignup_CreateMember_TxRollbackOnError vérifie que la TX est rollback complète
// quand user_roles échoue (FK violation : rôle 'member' absent).
func TestSignup_CreateMember_TxRollbackOnError(t *testing.T) {
	db := newTestDB(t)
	// Intentionnellement sans seedRoles → 'member' n'existe pas → FK violation sur user_roles
	ctx := context.Background()

	_, err := createMember(ctx, db, "test@example.com", "Test User", "adherent", "{}", "127.0.0.1", []byte("secret"))
	if err == nil {
		t.Fatal("createMember devrait retourner une erreur (FK violation sur user_roles)")
	}

	var userCount int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount)
	if userCount != 0 {
		t.Errorf("TX rollback attendu : users doit être vide, got %d", userCount)
	}

	var signupCount int
	db.QueryRow(`SELECT COUNT(*) FROM signups`).Scan(&signupCount)
	if signupCount != 0 {
		t.Errorf("TX rollback attendu : signups doit être vide, got %d", signupCount)
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
	_ = lvl2ID

	// Vérifier que depth = ForumMaxDepth-1
	parentNode, err := treeStore.GetBySlug(ctx, "lvl2-test")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if parentNode.Depth != ForumMaxDepth-1 {
		t.Fatalf("depth attendu %d got %d", ForumMaxDepth-1, parentNode.Depth)
	}

	deps := app.AppDeps{DB: db}
	handler := handleForumReply(deps)

	// Injecter l'utilisateur et le paramètre chi {slug}
	user := &auth.User{ID: "user-test", Email: "u@test.com", Roles: []string{"member"}}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "lvl2-test")

	r := httptest.NewRequest(http.MethodPost, "/forum/lvl2-test/reply", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("profondeur max : attendu 400, got %d", w.Code)
	}
}
