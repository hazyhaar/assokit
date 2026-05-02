// CLAUDE:SUMMARY Tests gardiens RequestID middleware — UUID unique par requête, propagation ctx, header response (M-ASSOKIT-AUDIT-FIX-1).
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMiddlewareRequestID_GeneratesUUIDPerRequest : 2 requêtes consécutives → 2 req_id distincts.
func TestMiddlewareRequestID_GeneratesUUIDPerRequest(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r1 := httptest.NewRequest("GET", "/", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, r1)
	id1 := w1.Header().Get("X-Request-ID")

	r2 := httptest.NewRequest("GET", "/", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	id2 := w2.Header().Get("X-Request-ID")

	if id1 == "" {
		t.Error("requête 1 : header X-Request-ID vide")
	}
	if id2 == "" {
		t.Error("requête 2 : header X-Request-ID vide")
	}
	if id1 == id2 {
		t.Errorf("req_ids identiques entre 2 requêtes : %s", id1)
	}
	if len(id1) < 32 {
		t.Errorf("req_id trop court (attendu UUID format) : %q", id1)
	}
}

// TestMiddlewareRequestID_PropagatedToCtx : handler récupère le req_id via RequestIDFromContext.
func TestMiddlewareRequestID_PropagatedToCtx(t *testing.T) {
	var captured string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if captured == "" {
		t.Fatal("RequestIDFromContext retourne \"\" : ctx mal propagé")
	}
	if captured != w.Header().Get("X-Request-ID") {
		t.Errorf("ctx req_id %q != header %q", captured, w.Header().Get("X-Request-ID"))
	}
}

// TestMiddlewareRequestID_ClientHeaderIgnored : header X-Request-ID entrant est ignoré (trust serveur).
func TestMiddlewareRequestID_ClientHeaderIgnored(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", "MALICIOUS-CLIENT-VALUE")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	got := w.Header().Get("X-Request-ID")
	if got == "MALICIOUS-CLIENT-VALUE" {
		t.Error("header client X-Request-ID utilisé (bypass possible) — doit être généré serveur")
	}
}

// TestRequestIDFromContext_AbsentReturnsEmpty : handler hors chaîne middleware → "".
func TestRequestIDFromContext_AbsentReturnsEmpty(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := RequestIDFromContext(r.Context()); got != "" {
		t.Errorf("RequestIDFromContext sans middleware = %q, attendu \"\"", got)
	}
}

// TestHashEmail_DeterministicAnd16Chars : même email → même hash, 16 chars hex.
func TestHashEmail_DeterministicAnd16Chars(t *testing.T) {
	h1 := HashEmail("alice@example.org")
	h2 := HashEmail("alice@example.org")
	h3 := HashEmail("bob@example.org")

	if h1 != h2 {
		t.Errorf("HashEmail non déterministe : %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("HashEmail collision : %q == %q", h1, h3)
	}
	if len(h1) != 16 {
		t.Errorf("HashEmail length = %d, attendu 16", len(h1))
	}
	// Vérifier que ce n'est pas l'email en clair
	if strings.Contains(h1, "alice") || strings.Contains(h1, "@") {
		t.Errorf("HashEmail leak email en clair : %q", h1)
	}
}

// TestHashIP_DependsOnSecret : même IP, secrets différents → hashes différents.
func TestHashIP_DependsOnSecret(t *testing.T) {
	s1 := []byte("secret-A-32-bytes-long-padding-1")
	s2 := []byte("secret-B-32-bytes-long-padding-2")

	h1 := HashIP("192.168.1.1", s1)
	h2 := HashIP("192.168.1.1", s2)
	h3 := HashIP("192.168.1.1", s1)

	if h1 == h2 {
		t.Error("HashIP même secret attendu mais hashes égaux entre secrets distincts")
	}
	if h1 != h3 {
		t.Errorf("HashIP non déterministe : %q != %q", h1, h3)
	}
	if strings.Contains(h1, "192") || strings.Contains(h1, ".") {
		t.Errorf("HashIP leak IP brute : %q", h1)
	}
}
