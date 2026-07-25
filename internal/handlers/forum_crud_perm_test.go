// CLAUDE:SUMMARY Test : POST /forum/{slug}/rename et /delete gardés RequirePerm(write,node-forum) — admin/member OK, reader 403 ; rename change le titre ; delete soft-delete en cascade (nœud + enfant).
package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	tree "github.com/hazyhaar/nodetree"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/bootstrap"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
)

// forumCrudDeps prépare une base avec le forum bootstrappé et un logger muet.
func forumCrudDeps(t *testing.T) (app.AppDeps, *tree.Store) {
	t.Helper()
	db := newTestDB(t)
	if err := bootstrap.BootstrapForumPerms(db); err != nil {
		t.Fatalf("BootstrapForumPerms: %v", err)
	}
	deps := app.AppDeps{DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return deps, &tree.Store{DB: db}
}

// guardedForum enveloppe un handler par la même garde que les routes forum.
func guardedForum(deps app.AppDeps, h http.HandlerFunc) http.Handler {
	return middleware.RequirePerm(deps.DB, perms.PermWrite, func(*http.Request) string {
		return "node-forum"
	})(h)
}

// postForum forge une requête POST /forum/{slug}/<verbe> avec slug en route et
// éventuellement un utilisateur en contexte, puis l'exécute contre h.
func postForum(h http.Handler, slug, formBody string, user *identity.User) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	r := httptest.NewRequest(http.MethodPost, "/forum/"+slug+"/x", strings.NewReader(formBody))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != nil {
		r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	}
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var (
	userAdmin  = &identity.User{ID: "u-admin", Email: "a@test.com", Roles: []string{"admin"}}
	userMember = &identity.User{ID: "u-member", Email: "m@test.com", Roles: []string{"member"}}
	userReader = &identity.User{ID: "u-reader", Email: "r@test.com", Roles: []string{"reader"}}
)

// getForumForm forge une requête GET /forum/{slug}/<form> avec slug en route et
// éventuellement un utilisateur en contexte, puis l'exécute contre h.
func getForumForm(h http.Handler, slug string, user *identity.User) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	r := httptest.NewRequest(http.MethodGet, "/forum/"+slug+"/form", nil)
	if user != nil {
		r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	}
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestForumNewForms_Guarded : les GET des formulaires de création (new topic,
// new-question, new-branch) sont gardés par la MÊME garde d'écriture que leurs
// POST — cohérence vue<->handler. Un visiteur non-connecté est redirigé (302),
// un connecté sans permission d'écriture est refusé (403), un admin franchit la
// garde (jamais 302/403).
func TestForumNewForms_Guarded(t *testing.T) {
	deps, store := forumCrudDeps(t)
	ctx := context.Background()
	root, _ := ensureForumRoot(ctx, store)
	catID, _ := store.Create(ctx, tree.Node{
		Slug: "cat-g", Type: "post", Title: "Cat",
		ParentID: sql.NullString{String: root.ID, Valid: true},
	})
	store.Create(ctx, tree.Node{ //nolint:errcheck
		Slug: "question-g", Type: "post", Title: "Question",
		ParentID: sql.NullString{String: catID, Valid: true},
	})

	forms := []struct {
		name    string
		handler http.HandlerFunc
		slug    string
	}{
		{"new-topic", handleForumNewTopicForm(deps), "new"},
		{"new-question", handleForumNewQuestionForm(deps), "cat-g"},
		{"new-branch", handleForumNewBranchForm(deps), "question-g"},
	}

	for _, f := range forms {
		t.Run(f.name+"/guest-redirect", func(t *testing.T) {
			w := getForumForm(guardedForum(deps, f.handler), f.slug, nil)
			if w.Code != http.StatusFound {
				t.Errorf("guest sur GET %s : code %d, want 302 (login)", f.name, w.Code)
			}
		})
		t.Run(f.name+"/reader-403", func(t *testing.T) {
			w := getForumForm(guardedForum(deps, f.handler), f.slug, userReader)
			if w.Code != http.StatusForbidden {
				t.Errorf("reader sur GET %s : code %d, want 403", f.name, w.Code)
			}
		})
		t.Run(f.name+"/admin-passes", func(t *testing.T) {
			w := getForumForm(guardedForum(deps, f.handler), f.slug, userAdmin)
			if w.Code == http.StatusForbidden || w.Code == http.StatusFound {
				t.Errorf("admin sur GET %s : code %d, ne devrait pas être bloqué par la garde", f.name, w.Code)
			}
		})
	}
}

// TestForumRename_ChangesTitle : un admin renomme un nœud ; le titre change en
// base, le slug reste stable.
func TestForumRename_ChangesTitle(t *testing.T) {
	deps, store := forumCrudDeps(t)
	ctx := context.Background()

	root, err := ensureForumRoot(ctx, store)
	if err != nil {
		t.Fatalf("ensureForumRoot: %v", err)
	}
	catID, err := store.Create(ctx, tree.Node{
		Slug:     "cat-a",
		Type:     "post",
		Title:    "Ancien titre",
		ParentID: sql.NullString{String: root.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	w := postForum(guardedForum(deps, handleForumRename(deps)), "cat-a", "title=Nouveau titre", userAdmin)
	if w.Code == http.StatusForbidden {
		t.Fatalf("admin renomme : 403 inattendu")
	}

	got, err := store.GetByID(ctx, catID)
	if err != nil {
		t.Fatalf("GetByID après rename: %v", err)
	}
	if got.Title != "Nouveau titre" {
		t.Errorf("titre = %q, want %q", got.Title, "Nouveau titre")
	}
	if got.Slug != "cat-a" {
		t.Errorf("slug = %q, want stable %q", got.Slug, "cat-a")
	}
}

// TestForumDelete_CascadeSoftDelete : un admin supprime une question ; le nœud
// ET son enfant (branche) reçoivent deleted_at (introuvables ensuite).
func TestForumDelete_CascadeSoftDelete(t *testing.T) {
	deps, store := forumCrudDeps(t)
	ctx := context.Background()

	root, _ := ensureForumRoot(ctx, store)
	catID, _ := store.Create(ctx, tree.Node{
		Slug: "cat-b", Type: "post", Title: "Catégorie",
		ParentID: sql.NullString{String: root.ID, Valid: true},
	})
	qID, _ := store.Create(ctx, tree.Node{
		Slug: "question-b", Type: "post", Title: "Question",
		ParentID: sql.NullString{String: catID, Valid: true},
	})
	branchID, _ := store.Create(ctx, tree.Node{
		Slug: "branch-b", Type: "post", Title: "Branche",
		ParentID: sql.NullString{String: qID, Valid: true},
	})

	w := postForum(guardedForum(deps, handleForumDelete(deps)), "question-b", "", userAdmin)
	if w.Code == http.StatusForbidden {
		t.Fatalf("admin supprime : 403 inattendu")
	}

	if _, err := store.GetByID(ctx, qID); err != tree.ErrNotFound {
		t.Errorf("question devrait être soft-deleted (NotFound), got %v", err)
	}
	if _, err := store.GetByID(ctx, branchID); err != tree.ErrNotFound {
		t.Errorf("branche enfant devrait être soft-deleted en cascade (NotFound), got %v", err)
	}
}

// TestForumCrud_PermByGrade : reader (sans write) refusé en 403 sur rename,
// delete et new ; admin et member franchissent la garde.
func TestForumCrud_PermByGrade(t *testing.T) {
	deps, store := forumCrudDeps(t)
	ctx := context.Background()
	root, _ := ensureForumRoot(ctx, store)
	store.Create(ctx, tree.Node{ //nolint:errcheck
		Slug: "cat-c", Type: "post", Title: "Cat",
		ParentID: sql.NullString{String: root.ID, Valid: true},
	})

	cases := []struct {
		name    string
		handler http.HandlerFunc
		slug    string
		body    string
	}{
		{"rename", handleForumRename(deps), "cat-c", "title=Renommé"},
		{"delete", handleForumDelete(deps), "cat-c", ""},
		{"new", handleForumCreateTopic(deps), "new", "title=Cat&body=Corps"},
	}

	for _, c := range cases {
		t.Run(c.name+"/reader-403", func(t *testing.T) {
			w := postForum(guardedForum(deps, c.handler), c.slug, c.body, userReader)
			if w.Code != http.StatusForbidden {
				t.Errorf("reader sur %s : code %d, want 403", c.name, w.Code)
			}
		})
		t.Run(c.name+"/guest-redirect", func(t *testing.T) {
			w := postForum(guardedForum(deps, c.handler), c.slug, c.body, nil)
			if w.Code != http.StatusFound {
				t.Errorf("guest sur %s : code %d, want 302 (login)", c.name, w.Code)
			}
		})
	}

	// admin et member franchissent la garde (jamais 403) sur rename.
	for _, u := range []*identity.User{userAdmin, userMember} {
		// Re-crée la catégorie (un delete précédent a pu la retirer).
		store.Create(ctx, tree.Node{ //nolint:errcheck
			Slug: "cat-" + u.ID, Type: "post", Title: "Cat",
			ParentID: sql.NullString{String: root.ID, Valid: true},
		})
		w := postForum(guardedForum(deps, handleForumRename(deps)), "cat-"+u.ID, "title=OK", u)
		if w.Code == http.StatusForbidden {
			t.Errorf("%s sur rename : 403 inattendu (devrait franchir la garde write)", u.Roles[0])
		}
	}
}
