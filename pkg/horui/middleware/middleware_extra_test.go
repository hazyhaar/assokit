package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func TestCSRFValidToken(t *testing.T) {
	secret := []byte("csrf-secret")

	// Obtenir un token depuis un GET
	var token string
	handler := middleware.CSRF(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = middleware.CSRFToken(r.Context())
		w.WriteHeader(200)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if token == "" {
		t.Fatal("token CSRF absent après GET")
	}

	// POST avec bon token dans le form + cookie
	csrfCookies := w.Result().Cookies()
	body := strings.NewReader("_csrf=" + token)
	r2 := httptest.NewRequest("POST", "/", body)
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range csrfCookies {
		r2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != 200 {
		t.Errorf("POST avec bon token: want 200 got %d", w2.Code)
	}
}

func TestCSRFHeaderToken(t *testing.T) {
	secret := []byte("csrf-secret")

	// Obtenir cookie CSRF
	var token string
	handler := middleware.CSRF(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = middleware.CSRFToken(r.Context())
		w.WriteHeader(200)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	csrfCookies := w.Result().Cookies()

	// POST avec token dans le header
	r2 := httptest.NewRequest("POST", "/", strings.NewReader(""))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.Header.Set("X-CSRF-Token", token)
	for _, c := range csrfCookies {
		r2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != 200 {
		t.Errorf("POST avec header CSRF: want 200 got %d", w2.Code)
	}
}
