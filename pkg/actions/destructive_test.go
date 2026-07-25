package actions_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
	"github.com/hazyhaar/assokit/pkg/rbac"
)

func TestDestructive_WithoutConfirmRejected(t *testing.T) {
	db := openDB(t)
	deps := depsWithDB(db)
	reg, svc := destructiveTestRegistry(t, db)

	r := chi.NewRouter()
	r.Use(middleware.RBAC(svc))
	actions.MountHTTP(r, deps, reg)

	form := url.Values{}
	form.Set("id", "target-42")
	req := httptest.NewRequest(http.MethodPost, "/admin/actions/test.destructive", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(middleware.ContextWithUser(
		perms.ContextWithUserID(perms.ContextWithService(req.Context(), svc), "u-dest"),
		&identity.User{ID: "u-dest"},
	))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "confirmation typée requise") {
		t.Fatalf("bandeau erreur attendu, body=%s", w.Body.String())
	}
}

func TestDestructive_WithConfirmPasses(t *testing.T) {
	db := openDB(t)
	deps := depsWithDB(db)
	reg, svc := destructiveTestRegistry(t, db)

	r := chi.NewRouter()
	r.Use(middleware.RBAC(svc))
	actions.MountHTTP(r, deps, reg)

	form := url.Values{}
	form.Set("id", "target-42")
	form.Set("_destructive_confirm", "target-42")
	req := httptest.NewRequest(http.MethodPost, "/admin/actions/test.destructive", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(middleware.ContextWithUser(
		perms.ContextWithUserID(perms.ContextWithService(req.Context(), svc), "u-dest"),
		&identity.User{ID: "u-dest"},
	))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("succès attendu, body=%s", w.Body.String())
	}

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM data_access_log WHERE action = 'test.destructive'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit destructive: count=%d", n)
	}
}

func TestDestructive_OldWritePermDenied403(t *testing.T) {
	db := openDB(t)
	reg := actions.NewRegistry()
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "test.destructive",
		Title:        "Test destroy",
		RequiredPerm: "test.destroy",
		Destructive:  true,
		ParamsSchema: actions.MustSchema(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`),
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			return actions.Result{Status: "ok"}, nil
		},
	})

	svc := &rbac.Service{Store: &rbac.Store{DB: db}, Cache: &rbac.Cache{}}
	ctx := context.Background()
	gID, _ := svc.Store.CreateGrade(ctx, "write-only")
	pWrite, _ := svc.Store.EnsurePermission(ctx, "test.write", "")
	pDestroy, _ := svc.Store.EnsurePermission(ctx, "test.destroy", "")
	svc.Store.GrantPerm(ctx, gID, pWrite)      //nolint:errcheck
	svc.Store.AssignGrade(ctx, "u-write", gID) //nolint:errcheck
	svc.Recompute(ctx, "u-write")              //nolint:errcheck
	_ = pDestroy

	r := chi.NewRouter()
	r.Use(middleware.RBAC(svc))
	r.With(perms.Required("test.destroy")).Post("/admin/actions/test.destructive", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/actions/test.destructive", nil)
	req = req.WithContext(middleware.ContextWithUser(
		perms.ContextWithUserID(perms.ContextWithService(req.Context(), svc), "u-write"),
		&identity.User{ID: "u-write"},
	))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("attendu 403 avec test.write seul, obtenu %d", w.Code)
	}
}

func destructiveTestRegistry(t *testing.T, db *sql.DB) (*actions.Registry, *rbac.Service) {
	t.Helper()
	reg := actions.NewRegistry()
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "test.destructive",
		Title:        "Test destroy",
		RequiredPerm: "test.destroy",
		Destructive:  true,
		ParamsSchema: actions.MustSchema(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`),
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			return actions.Result{Status: "ok", Message: "ok"}, nil
		},
	})
	svc := &rbac.Service{Store: &rbac.Store{DB: db}, Cache: &rbac.Cache{}}
	ctx := context.Background()
	gID, _ := svc.Store.CreateGrade(ctx, "dest-grade")
	pID, _ := svc.Store.EnsurePermission(ctx, "test.destroy", "")
	svc.Store.GrantPerm(ctx, gID, pID)        //nolint:errcheck
	svc.Store.AssignGrade(ctx, "u-dest", gID) //nolint:errcheck
	svc.Recompute(ctx, "u-dest")              //nolint:errcheck
	return reg, svc
}
