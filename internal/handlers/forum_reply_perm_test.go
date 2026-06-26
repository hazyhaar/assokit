// CLAUDE:SUMMARY Test : POST /forum/{slug}/reply gardé par RequirePerm(write,node-forum) — admin/member/moderator écrivent, guest refusé.
package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	tree "github.com/hazyhaar/assokit/internal/nodetree"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/bootstrap"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
)

// TestForumReply_PermByGrade couvre le couple permission×route de la réponse
// forum : POST /forum/{slug}/reply est gardé par
// RequirePerm(PermWrite, "node-forum") (cf. routes.go). Ce test serait devenu
// ROUGE quand le grade « moderator » n'avait pas la permission write (le 403
// silencieux d'origine, incident Dominique 2026-06-25) ; il est VERT depuis que
// BootstrapForumPerms grave moderator. Réutilise le même middleware que la route.
func TestForumReply_PermByGrade(t *testing.T) {
	db := newTestDB(t)

	// Pose les permissions write sur node-forum pour admin/member/moderator.
	if err := bootstrap.BootstrapForumPerms(db); err != nil {
		t.Fatalf("BootstrapForumPerms: %v", err)
	}

	treeStore := &tree.Store{DB: db}
	ctx := context.Background()

	// Nœud parent réel (profondeur 0 < ForumMaxDepth-1) sur lequel répondre.
	if _, err := treeStore.Create(ctx, tree.Node{
		Slug:  "general-test",
		Type:  "post",
		Title: "Question générale",
	}); err != nil {
		t.Fatalf("create parent node: %v", err)
	}

	deps := app.AppDeps{DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// Wrapping identique à routes.go : RequirePerm(write, node-forum) → handleForumReply.
	guarded := middleware.RequirePerm(deps.DB, perms.PermWrite, func(*http.Request) string {
		return "node-forum"
	})(handleForumReply(deps))

	tests := []struct {
		name      string
		user      *identity.User
		wantCode  int
		wantAllow bool // l'écriture est-elle censée passer la garde de permission ?
	}{
		{
			name:      "admin écrit",
			user:      &identity.User{ID: "u-admin", Email: "a@test.com", Roles: []string{"admin"}},
			wantCode:  http.StatusSeeOther, // 303 : réponse créée, redirect /forum/{slug}
			wantAllow: true,
		},
		{
			name:      "member écrit",
			user:      &identity.User{ID: "u-member", Email: "m@test.com", Roles: []string{"member"}},
			wantCode:  http.StatusSeeOther,
			wantAllow: true,
		},
		{
			name:      "moderator écrit",
			user:      &identity.User{ID: "u-mod", Email: "mod@test.com", Roles: []string{"moderator"}},
			wantCode:  http.StatusSeeOther,
			wantAllow: true,
		},
		{
			// Utilisateur authentifié mais d'un grade SANS droit d'écriture sur
			// node-forum : exerce directement le chemin de refus 403 de RequirePerm
			// (distinct du redirect 302 du guest non authentifié).
			name:      "grade sans write refusé",
			user:      &identity.User{ID: "u-reader", Email: "r@test.com", Roles: []string{"reader"}},
			wantCode:  http.StatusForbidden, // 403 : UserCan false → Accès refusé
			wantAllow: false,
		},
		{
			name:      "guest non authentifié refusé",
			user:      nil,              // pas d'utilisateur en contexte
			wantCode:  http.StatusFound, // 302 : RequirePerm redirige vers /login
			wantAllow: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("slug", "general-test")

			body := strings.NewReader("body=Bonjour")
			r := httptest.NewRequest(http.MethodPost, "/forum/general-test/reply", body)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.user != nil {
				r = r.WithContext(middleware.ContextWithUser(r.Context(), tc.user))
			}
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			guarded.ServeHTTP(w, r)

			if w.Code != tc.wantCode {
				t.Errorf("code = %d, want %d", w.Code, tc.wantCode)
			}
			// Une garde franchie ne doit JAMAIS produire un 403 (le bug d'origine).
			if tc.wantAllow && w.Code == http.StatusForbidden {
				t.Errorf("403 inattendu : le grade aurait dû passer la garde de permission write")
			}
			// Un refus de garde ne doit JAMAIS produire un 2xx/303 (écriture passée).
			if !tc.wantAllow && (w.Code == http.StatusSeeOther || (w.Code >= 200 && w.Code < 300)) {
				t.Errorf("écriture passée alors qu'elle aurait dû être refusée (code %d)", w.Code)
			}
		})
	}
}
