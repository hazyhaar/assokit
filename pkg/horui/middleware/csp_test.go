// CLAUDE:SUMMARY Tests gardiens CSP injectable — non-régression du défaut durci (negcriterion N4) + composition par CSPExtra + nonce per-request.
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCSPDefaultNoNonce : CSP de base sans nonce : script-src = 'self' seulement
// (pas de 'unsafe-inline'). Vérifie que le durcissement historique est préservé
// hormis le remplacement volontaire de 'unsafe-inline' par le nonce.
func TestCSPDefaultNoNonce(t *testing.T) {
	got := buildCSPWithNonce(CSPExtra{}, "")
	want := "script-src 'self'"
	if !strings.Contains(got, want) {
		t.Errorf("CSP sans nonce : script-src devrait contenir %q, CSP: %s", want, got)
	}
	// Vérifier que 'unsafe-inline' a disparu de script-src
	if strings.Contains(got, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("script-src contient encore 'unsafe-inline' : CSP: %s", got)
	}
	// style-src garde 'unsafe-inline' (styles inline, risque limité)
	if !strings.Contains(got, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("style-src devrait conserver 'unsafe-inline' : CSP: %s", got)
	}
}

// TestCSPWithNonce : la CSP avec nonce injecte 'nonce-{nonce}' et 'strict-dynamic'
// dans script-src.
func TestCSPWithNonce(t *testing.T) {
	got := buildCSPWithNonce(CSPExtra{}, "testnonce123")
	want := "script-src 'self' 'nonce-testnonce123' 'strict-dynamic'"
	if !strings.Contains(got, want) {
		t.Errorf("CSP avec nonce : script-src ne contient pas %q, CSP: %s", want, got)
	}
}

// TestSecurityHeadersInjectsNonce : le middleware génère un nonce, le place dans
// la CSP, et l'injecte dans le contexte pour les templates.
func TestSecurityHeadersInjectsNonce(t *testing.T) {
	rec := httptest.NewRecorder()
	nonceSeen := ""
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceSeen = NonceFromContext(r.Context())
	}))
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("aucune CSP posée")
	}
	if !strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP ne contient pas de nonce : %s", csp)
	}
	if !strings.Contains(csp, "'strict-dynamic'") {
		t.Errorf("CSP ne contient pas 'strict-dynamic' : %s", csp)
	}
	if nonceSeen == "" {
		t.Error("nonce absent du contexte")
	}
	// Vérifier que le nonce dans le contexte correspond à celui dans la CSP
	if !strings.Contains(csp, "'nonce-"+nonceSeen+"'") {
		t.Errorf("nonce contexte %q != nonce CSP", nonceSeen)
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
	got := buildCSPWithNonce(extra, "")

	if !strings.Contains(got, "script-src 'self' https://cdn.jsdelivr.net;") {
		t.Errorf("script-src non complété : %s", got)
	}
	if !strings.Contains(got, "style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net;") {
		t.Errorf("style-src non complété : %s", got)
	}
	if !strings.Contains(got, "connect-src 'self' https://data.geopf.fr;") {
		t.Errorf("connect-src non complété : %s", got)
	}
	if !strings.Contains(got, "worker-src 'self' blob:") {
		t.Errorf("worker-src non matérialisé : %s", got)
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
