package middleware_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/internal/chassis"
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
	db.Exec(`INSERT INTO roles(id,label) VALUES('member','Member') ON CONFLICT DO NOTHING`)
	return db
}

func TestHTMXDetection(t *testing.T) {
	handler := middleware.HTMX(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if middleware.IsHTMX(r.Context()) {
			w.WriteHeader(299)
		} else {
			w.WriteHeader(200)
		}
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 299 {
		t.Errorf("HTMX request: want 299 got %d", w.Code)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != 200 {
		t.Errorf("non-HTMX request: want 200 got %d", w2.Code)
	}
}

func TestSessionCookieSignVerify(t *testing.T) {
	secret := []byte("test-secret-key")
	w := httptest.NewRecorder()
	middleware.SetSessionCookie(w, "user-123", secret, false)

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("aucun cookie posé")
	}
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "nps_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("cookie nps_session absent")
	}

	// Vérifie que le middleware Auth accepte le cookie
	db := newTestDB(t)
	store := &auth.Store{DB: db}
	u, _ := store.Register(context.Background(), "test@nps.fr", "pass", "Test")

	w2 := httptest.NewRecorder()
	middleware.SetSessionCookie(w2, u.ID, secret, false)
	realCookies := w2.Result().Cookies()

	authMiddleware := middleware.Auth(db, secret)
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user != nil && user.ID == u.ID {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(401)
		}
	}))

	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range realCookies {
		r.AddCookie(c)
	}
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, r)
	if w3.Code != 200 {
		t.Errorf("auth avec cookie valide: want 200 got %d", w3.Code)
	}
}

func TestSessionCookieTamperedRejected(t *testing.T) {
	secret := []byte("test-secret-key")
	db := newTestDB(t)
	store := &auth.Store{DB: db}
	u, _ := store.Register(context.Background(), "tamper@nps.fr", "pass", "Tamper")

	w := httptest.NewRecorder()
	middleware.SetSessionCookie(w, u.ID, secret, false)

	authMiddleware := middleware.Auth(db, secret)
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			w.WriteHeader(401)
		} else {
			w.WriteHeader(200)
		}
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "nps_session", Value: "tampered-value"})
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r)
	if w2.Code != 401 {
		t.Errorf("cookie tampered: want 401 got %d", w2.Code)
	}
}

func TestCSRFProtection(t *testing.T) {
	secret := []byte("csrf-secret")
	handler := middleware.CSRF(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// GET passe sans token
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("GET sans token: want 200 got %d", w.Code)
	}

	// POST sans token → 403
	r2 := httptest.NewRequest("POST", "/", strings.NewReader(""))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != 403 {
		t.Errorf("POST sans token: want 403 got %d", w2.Code)
	}
}

func TestFlashRoundtrip(t *testing.T) {
	handler := middleware.Flash(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msgs := middleware.PopFlash(r.Context())
		if len(msgs) > 0 {
			w.WriteHeader(299)
		} else {
			w.WriteHeader(200)
		}
	}))

	// Sans flash → 200
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("sans flash: want 200 got %d", w.Code)
	}

	// Avec flash cookie → 299
	w2 := httptest.NewRecorder()
	middleware.PushFlash(w2, "info", "message test")
	flashCookies := w2.Result().Cookies()

	r2 := httptest.NewRequest("GET", "/", nil)
	for _, c := range flashCookies {
		r2.AddCookie(c)
	}
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, r2)
	if w3.Code != 299 {
		t.Errorf("avec flash: want 299 got %d", w3.Code)
	}
}

func TestUserFromContextNil(t *testing.T) {
	u := middleware.UserFromContext(context.Background())
	if u != nil {
		t.Error("contexte vide: user devrait être nil")
	}
}
