// CLAUDE:SUMMARY Tests helpers RBAC perms : Required, Has, IfHas, anonyme (M-ASSOKIT-RBAC-3).
package perms_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

func rawComponent(s string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

func openRBACPermsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	return db
}

func newRBACSvc(t *testing.T) (*rbac.Service, *sql.DB) {
	t.Helper()
	db := openRBACPermsDB(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: db}, Cache: &rbac.Cache{}}
	return svc, db
}

// ctxWithPerms injecte service+userID et assigne gradeID à userID avec permName.
func ctxWithPerms(ctx context.Context, svc *rbac.Service, userID, permName string) context.Context {
	ctx = perms.ContextWithService(ctx, svc)
	ctx = perms.ContextWithUserID(ctx, userID)
	return ctx
}

// TestPerms_Required403WithoutPerm_NextWithPerm : Required() bloque 403 sans perm, passe avec.
func TestPerms_Required403WithoutPerm_NextWithPerm(t *testing.T) {
	svc, db := newRBACSvc(t)
	defer db.Close()
	ctx := context.Background()

	gID, _ := svc.Store.CreateGrade(ctx, "test-grade-req")
	pID, _ := svc.Store.EnsurePermission(ctx, "feedback.triage", "")
	svc.Store.GrantPerm(ctx, gID, pID)   //nolint:errcheck
	svc.AssignGrade(ctx, "user-req", gID) //nolint:errcheck

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := perms.Required("feedback.triage")(okHandler)

	// Sans user → 403
	rNoUser := httptest.NewRequest("GET", "/", nil)
	rNoUser = rNoUser.WithContext(perms.ContextWithService(rNoUser.Context(), svc))
	wNoUser := httptest.NewRecorder()
	mw.ServeHTTP(wNoUser, rNoUser)
	if wNoUser.Code != http.StatusForbidden {
		t.Errorf("sans user: want 403 got %d", wNoUser.Code)
	}

	// Avec user sans perm → 403
	rWrong := httptest.NewRequest("GET", "/", nil)
	rWrong = rWrong.WithContext(ctxWithPerms(rWrong.Context(), svc, "user-noperm", "feedback.triage"))
	wWrong := httptest.NewRecorder()
	mw.ServeHTTP(wWrong, rWrong)
	if wWrong.Code != http.StatusForbidden {
		t.Errorf("user sans perm: want 403 got %d", wWrong.Code)
	}

	// Avec user ayant perm → 200
	rOK := httptest.NewRequest("GET", "/", nil)
	rOK = rOK.WithContext(ctxWithPerms(rOK.Context(), svc, "user-req", "feedback.triage"))
	wOK := httptest.NewRecorder()
	mw.ServeHTTP(wOK, rOK)
	if wOK.Code != http.StatusOK {
		t.Errorf("user avec perm: want 200 got %d", wOK.Code)
	}
}

// TestPerms_HasReadsCacheHotPath : Has() retourne true/false selon les perms réelles.
func TestPerms_HasReadsCacheHotPath(t *testing.T) {
	svc, db := newRBACSvc(t)
	defer db.Close()
	ctx := context.Background()

	gID, _ := svc.Store.CreateGrade(ctx, "grade-has")
	pID, _ := svc.Store.EnsurePermission(ctx, "hot.perm", "")
	svc.Store.GrantPerm(ctx, gID, pID)    //nolint:errcheck
	svc.AssignGrade(ctx, "user-has", gID) //nolint:errcheck

	reqCtx := ctxWithPerms(ctx, svc, "user-has", "hot.perm")

	// Premier appel : charge depuis L2
	if !perms.Has(reqCtx, "hot.perm") {
		t.Error("Has: devrait être true pour user-has/hot.perm")
	}
	// Deuxième appel : hot path L1
	if !perms.Has(reqCtx, "hot.perm") {
		t.Error("Has (cache): devrait être true")
	}
	// Perm absente
	if perms.Has(reqCtx, "other.perm") {
		t.Error("Has: devrait être false pour perm inconnue")
	}
}

// TestPerms_CtxWithoutUserReturnsForbidden : anonyme (pas userID) → Has false + Required 403.
func TestPerms_CtxWithoutUserReturnsForbidden(t *testing.T) {
	svc, db := newRBACSvc(t)
	defer db.Close()
	ctx := context.Background()

	// Seulement service, pas d'userID
	ctx = perms.ContextWithService(ctx, svc)

	if perms.Has(ctx, "any.perm") {
		t.Error("Has anonyme: devrait être false")
	}

	mw := perms.Required("any.perm")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("Required anonyme: want 403 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "any.perm") {
		t.Errorf("corps 403 devrait mentionner la perm: %q", w.Body.String())
	}
}

// TestPerms_IfHasRendersContentOnlyWhenAllowed : IfHas rend content si perm présente, rien sinon.
func TestPerms_IfHasRendersContentOnlyWhenAllowed(t *testing.T) {
	svc, db := newRBACSvc(t)
	defer db.Close()
	ctx := context.Background()

	gID, _ := svc.Store.CreateGrade(ctx, "grade-ifhas")
	pID, _ := svc.Store.EnsurePermission(ctx, "ifhas.perm", "")
	svc.Store.GrantPerm(ctx, gID, pID)       //nolint:errcheck
	svc.AssignGrade(ctx, "user-ifhas", gID) //nolint:errcheck

	allowedCtx := ctxWithPerms(ctx, svc, "user-ifhas", "ifhas.perm")
	deniedCtx := ctxWithPerms(ctx, svc, "user-denied", "ifhas.perm")

	content := rawComponent("<span>secret</span>")

	// Avec perm : contenu rendu
	var buf strings.Builder
	if err := perms.IfHas(allowedCtx, "ifhas.perm", content).Render(allowedCtx, &buf); err != nil {
		t.Fatalf("IfHas allowed render: %v", err)
	}
	if !strings.Contains(buf.String(), "secret") {
		t.Errorf("IfHas allowed: contenu attendu, got %q", buf.String())
	}

	// Sans perm : rien rendu
	buf.Reset()
	if err := perms.IfHas(deniedCtx, "ifhas.perm", content).Render(deniedCtx, &buf); err != nil {
		t.Fatalf("IfHas denied render: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("IfHas denied: vide attendu, got %q", buf.String())
	}
}
