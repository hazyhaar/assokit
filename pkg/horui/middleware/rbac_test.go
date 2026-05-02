// CLAUDE:SUMMARY Tests middleware RBAC : injection service ctx, anonyme (M-ASSOKIT-RBAC-3).
package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

func contextWithUser(ctx context.Context, u *auth.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// TestMiddlewareRBAC_InjectsServiceInCtx : RBAC() injecte le service en ctx.
func TestMiddlewareRBAC_InjectsServiceInCtx(t *testing.T) {
	svc := &rbac.Service{Store: &rbac.Store{}, Cache: &rbac.Cache{}}

	var gotSvc *rbac.Service
	handler := RBAC(svc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotSvc = perms.ServiceFromContext(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(contextWithUser(r.Context(), &auth.User{ID: "user-123"}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if gotSvc == nil {
		t.Fatal("ServiceFromContext: nil après RBAC middleware")
	}
	if gotSvc != svc {
		t.Error("ServiceFromContext: mauvais service")
	}
}

// TestMiddlewareRBAC_InjectsUserID : RBAC() extrait l'ID de l'auth.User et l'injecte.
func TestMiddlewareRBAC_InjectsUserID(t *testing.T) {
	svc := &rbac.Service{Store: &rbac.Store{}, Cache: &rbac.Cache{}}

	var gotUID string
	handler := RBAC(svc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUID = perms.UserID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(contextWithUser(r.Context(), &auth.User{ID: "user-abc"}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if gotUID != "user-abc" {
		t.Errorf("userID: want user-abc got %q", gotUID)
	}
}

// TestMiddlewareRBAC_AnonymousNoUserID : sans auth.User, userID reste "".
func TestMiddlewareRBAC_AnonymousNoUserID(t *testing.T) {
	svc := &rbac.Service{Store: &rbac.Store{}, Cache: &rbac.Cache{}}

	var gotUID string
	handler := RBAC(svc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUID = perms.UserID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	// Pas de contextWithUser → user = nil
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if gotUID != "" {
		t.Errorf("userID anonyme: want \"\" got %q", gotUID)
	}
}
