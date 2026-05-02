// CLAUDE:SUMMARY Tests gardiens admin panel concurrent autosave — last-write-wins + atomicité (M-ASSOKIT-AUDIT-FIX-2).
package adminpanel_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	adminpanel "github.com/hazyhaar/assokit/internal/handlers/admin_panel"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func newConcurrentDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	// Insérer l'utilisateur admin-test pour que branding.Set (FK updated_by) passe.
	if _, err := db.Exec(
		`INSERT INTO users(id, email, password_hash, display_name) VALUES('admin-test','admin@test.com','x','Admin Test')`,
	); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return app.AppDeps{DB: db, Logger: slog.Default()}
}

func newConcurrentRouter(deps app.AppDeps) chi.Router {
	r := chi.NewRouter()
	adminpanel.Mount(r, deps)
	return r
}

func newSaveFieldReq(key, value string) *http.Request {
	form := url.Values{}
	form.Set("key", key)
	form.Set("value", value)
	req := httptest.NewRequest(http.MethodPost, "/admin/panel/save-field", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u := &auth.User{ID: "admin-test", Email: "admin@test.com", Roles: []string{"admin"}}
	return req.WithContext(middleware.ContextWithUser(req.Context(), u))
}

// TestAdminPanel_ConcurrentAutosaveLastWriteWins : 2 goroutines POST simultanées,
// l'état final doit être l'une des deux valeurs (cohérence — pas de torn write).
func TestAdminPanel_ConcurrentAutosaveLastWriteWins(t *testing.T) {
	deps := newConcurrentDeps(t)
	r := newConcurrentRouter(deps)

	const key = "identite.nom_asso"
	valA := "Asso Alpha"
	valB := "Asso Beta"

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, newSaveFieldReq(key, valA))
	}()
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, newSaveFieldReq(key, valB))
	}()
	wg.Wait()

	var got string
	if err := deps.DB.QueryRowContext(context.Background(),
		`SELECT value FROM branding_kv WHERE key=?`, key).Scan(&got); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if got != valA && got != valB {
		t.Errorf("valeur finale incohérente : %q (attendu %q ou %q)", got, valA, valB)
	}
}

// TestAdminPanel_ConcurrentAutosaveAtomicity : N=10 goroutines, exactement 1 row finale.
func TestAdminPanel_ConcurrentAutosaveAtomicity(t *testing.T) {
	deps := newConcurrentDeps(t)
	r := newConcurrentRouter(deps)

	const key = "identite.sigle"
	const N = 10

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, newSaveFieldReq(key, fmt.Sprintf("V%d", i)))
		}(i)
	}
	wg.Wait()

	var count int
	if err := deps.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM branding_kv WHERE key=?`, key).Scan(&count); err != nil {
		t.Fatalf("COUNT: %v", err)
	}
	if count != 1 {
		t.Errorf("attendu exactement 1 row pour key=%q, got %d", key, count)
	}

	var got string
	deps.DB.QueryRow(`SELECT value FROM branding_kv WHERE key=?`, key).Scan(&got)
	if !strings.HasPrefix(got, "V") {
		t.Errorf("valeur finale = %q, attendu une des V0..V%d", got, N-1)
	}
}
