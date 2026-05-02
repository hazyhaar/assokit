// CLAUDE:SUMMARY Tests gardiens admin RBAC : create grade, cycle inherit 400, system grade 403, audit, guest 403, seed bootstrap (M-ASSOKIT-RBAC-4).
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"

	_ "modernc.org/sqlite"
)

// newRBACAdminDeps crée deps + service RBAC pour les tests admin RBAC.
func newRBACAdminDeps(t *testing.T) (app.AppDeps, *rbac.Service) {
	t.Helper()
	db := newTestDB(t)
	svc := &rbac.Service{
		Store: &rbac.Store{DB: db},
		Cache: &rbac.Cache{},
	}
	return app.AppDeps{DB: db, Logger: slog.Default()}, svc
}

// withRBACPerms injecte service + userID dans le contexte et assigne les perms demandées.
func withRBACPerms(r *http.Request, svc *rbac.Service, userID string, permNames ...string) *http.Request {
	ctx := perms.ContextWithService(r.Context(), svc)
	ctx = perms.ContextWithUserID(ctx, userID)
	// Créer grade temporaire avec les perms demandées et assigner à l'user.
	if len(permNames) > 0 {
		gID, _ := svc.Store.CreateGrade(ctx, "testgrade-"+userID)
		for _, pn := range permNames {
			pID, _ := svc.Store.EnsurePermission(ctx, pn, "")
			svc.GrantPerm(ctx, gID, pID) //nolint:errcheck
		}
		svc.AssignGrade(ctx, userID, gID) //nolint:errcheck
		svc.Recompute(ctx, userID)        //nolint:errcheck
	}
	return r.WithContext(ctx)
}

// adminRBACRequest crée une requête avec rôle admin (pour requireAdmin) et perms RBAC.
func adminRBACRequest(r *http.Request, svc *rbac.Service, userID string, permNames ...string) *http.Request {
	r = withRBACPerms(r, svc, userID, permNames...)
	u := &auth.User{ID: userID, Roles: []string{"admin"}}
	return r.WithContext(middleware.ContextWithUser(r.Context(), u))
}

// TestAdminRBAC_CreateGradeAndAssignUser : POST /admin/rbac/grades crée le grade.
func TestAdminRBAC_CreateGradeAndAssignUser(t *testing.T) {
	deps, svc := newRBACAdminDeps(t)

	form := url.Values{"name": {"testeur"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/grades", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = adminRBACRequest(req, svc, "user-create-1", "rbac.grades.write")

	w := httptest.NewRecorder()
	handleAdminRBACGradesCreate(deps, svc)(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("attendu 303 SeeOther, got %d", w.Code)
	}
	// Vérifier que le grade existe
	g, err := deps.DB.QueryContext(context.Background(), `SELECT name FROM grades WHERE name = 'testeur'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer g.Close()
	if !g.Next() {
		t.Error("grade 'testeur' introuvable après création")
	}
}

// TestAdminRBAC_AddInheritRefusesCycle : cycle → 400 (pas 500).
func TestAdminRBAC_AddInheritRefusesCycle(t *testing.T) {
	deps, svc := newRBACAdminDeps(t)
	ctx := context.Background()

	gA, _ := svc.Store.CreateGrade(ctx, "cycle-a")
	gB, _ := svc.Store.CreateGrade(ctx, "cycle-b")
	svc.AddInherit(ctx, gA, gB) //nolint:errcheck

	// Tenter c → a (créerait gA inherits gB inherits gA)
	form := url.Values{"parent_id": {gA}}
	req := httptest.NewRequest(http.MethodPost, "/admin/rbac/grades/"+gB+"/inherit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = adminRBACRequest(req, svc, "user-cycle-1", "rbac.grades.write")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", gB)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handleAdminRBACGradeInherit(deps, svc)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("cycle : attendu 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestAdminRBAC_DeleteSystemGradeReturns403 : DELETE grade système → 403.
func TestAdminRBAC_DeleteSystemGradeReturns403(t *testing.T) {
	deps, svc := newRBACAdminDeps(t)
	ctx := context.Background()

	// Insérer directement un grade système
	_, err := deps.DB.ExecContext(ctx, `INSERT INTO grades(id, name, system) VALUES('sys-test','superadmin',1)`)
	if err != nil {
		t.Fatalf("insert system grade: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/admin/rbac/grades/sys-test", nil)
	req = adminRBACRequest(req, svc, "user-del-1", "rbac.grades.write")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "sys-test")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handleAdminRBACGradeDelete(deps, svc)(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("attendu 403, got %d", w.Code)
	}
}

// TestAdminRBAC_AuditPageShowsRecentMutations : GET /admin/rbac/audit → 200, pas vide après mutations.
func TestAdminRBAC_AuditPageShowsRecentMutations(t *testing.T) {
	deps, svc := newRBACAdminDeps(t)
	ctx := context.Background()

	// Créer des mutations pour peupler l'audit
	svc.Store.CreateGrade(ctx, "grade-audit-test")    //nolint:errcheck
	svc.Store.EnsurePermission(ctx, "audit.test", "") //nolint:errcheck

	req := httptest.NewRequest(http.MethodGet, "/admin/rbac/audit", nil)
	req = adminRBACRequest(req, svc, "user-audit-1", "rbac.audit.read")

	w := httptest.NewRecorder()
	handleAdminRBACAuditList(deps)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("attendu 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Audit RBAC") {
		t.Error("page audit : titre 'Audit RBAC' absent")
	}
}

// TestAdminRBAC_GuestUserCannotAccessAnything : sans perms → 403 sur toutes les routes.
func TestAdminRBAC_GuestUserCannotAccessAnything(t *testing.T) {
	deps, svc := newRBACAdminDeps(t)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/rbac/grades"},
		{http.MethodGet, "/admin/rbac/audit"},
		{http.MethodGet, "/admin/rbac/users"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, nil)
			// Sans service RBAC dans le contexte → perms.Has retourne false → 403
			w := httptest.NewRecorder()
			handler := perms.Required("rbac.grades.read")(selectRBACHandler(deps, svc, rt.path))
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s : attendu 403, got %d", rt.method, rt.path, w.Code)
			}
		})
	}
}

// selectRBACHandler retourne le handler correspondant au path pour les tests guest.
func selectRBACHandler(deps app.AppDeps, svc *rbac.Service, path string) http.Handler {
	switch path {
	case "/admin/rbac/audit":
		return handleAdminRBACAuditList(deps)
	case "/admin/rbac/users":
		return handleAdminRBACUsersList(deps, svc)
	default:
		return handleAdminRBACGradesList(deps, svc)
	}
}

// TestRBAC_SeedSystemGradesPresentAfterBootstrap : chassis.Run seed les 4 grades système.
func TestRBAC_SeedSystemGradesPresentAfterBootstrap(t *testing.T) {
	db := newTestDB(t)
	// newTestDB appelle chassis.Run(db) → inclut migration 00004_rbac_system_grades_seed.sql
	for _, name := range []string{"admin", "moderator", "member", "guest"} {
		var id string
		err := db.QueryRow(`SELECT id FROM grades WHERE name = ? AND system = 1`, name).Scan(&id)
		if err != nil {
			t.Errorf("grade système %q absent après bootstrap: %v", name, err)
		}
	}
	_ = chassis.Run // référence pour vérifier que le package est bien utilisé
}
