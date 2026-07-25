// CLAUDE:SUMMARY Tests gardiens CSP injectable — non-régression du défaut durci (negcriterion N4) + composition par CSPExtra.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// defaultCSPLiteral est la CSP historique gelée. TestDefaultCSPUnchanged garantit
// que la sérialisation de la CSP par défaut reste octet-pour-octet identique.
const defaultCSPLiteral = "default-src 'self'; " +
	"img-src 'self' data: https:; " +
	"frame-src 'self' https://*.helloasso.com https://www.helloasso.com; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self' https://*.helloasso.com; " +
	"frame-ancestors 'none'; " +
	"upgrade-insecure-requests"

// TestDefaultCSPUnchanged : la CSP par défaut (extension zéro) est inchangée.
func TestDefaultCSPUnchanged(t *testing.T) {
	if got := buildCSP(CSPExtra{}); got != defaultCSPLiteral {
		t.Fatalf("CSP par défaut modifiée (negcriterion N4)\nattendu: %q\nobtenu:  %q", defaultCSPLiteral, got)
	}
}

// TestSecurityHeadersDefaultMatchesLiteral : le middleware sans extension pose
// exactement la chaîne CSP historique.
func TestSecurityHeadersDefaultMatchesLiteral(t *testing.T) {
	for _, mw := range []http.Handler{
		SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		SecurityHeadersWith(CSPExtra{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
	} {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if got := rec.Header().Get("Content-Security-Policy"); got != defaultCSPLiteral {
			t.Fatalf("CSP posée != défaut\nattendu: %q\nobtenu:  %q", defaultCSPLiteral, got)
		}
	}
}

// TestCSPExtraComposes : les sources additionnelles augmentent la directive ciblée
// sans toucher aux autres, et matérialisent worker-src (absente du défaut).
func TestCSPExtraComposes(t *testing.T) {
	extra := CSPExtra{
		ScriptSrc:  []string{"https://cdn.jsdelivr.net"},
		StyleSrc:   []string{"https://cdn.jsdelivr.net"},
		ConnectSrc: []string{"https://data.geopf.fr"},
		WorkerSrc:  []string{"blob:"},
	}
	got := buildCSP(extra)

	checks := []string{
		"script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net;",
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net;",
		"connect-src 'self' https://data.geopf.fr;",
		"worker-src 'self' blob:",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("CSP composée ne contient pas %q\nCSP: %s", want, got)
		}
	}
	// Directive non ciblée inchangée.
	if !strings.Contains(got, "font-src 'self' data:;") {
		t.Errorf("directive non ciblée altérée\nCSP: %s", got)
	}
	// object-src 'none' préservé (durcissement).
	if !strings.Contains(got, "object-src 'none';") {
		t.Errorf("object-src durci altéré\nCSP: %s", got)
	}
}

// TestCSPExtraIsZero : discriminant de la valeur zéro.
func TestCSPExtraIsZero(t *testing.T) {
	if !(CSPExtra{}).IsZero() {
		t.Error("CSPExtra{} devrait être zéro")
	}
	if (CSPExtra{ScriptSrc: []string{"x"}}).IsZero() {
		t.Error("CSPExtra avec ScriptSrc ne devrait pas être zéro")
	}
}
