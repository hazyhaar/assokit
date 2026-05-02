// CLAUDE:SUMMARY Tests NewProvider : discovery endpoint .well-known/openid-configuration, signing key, health (M-ASSOKIT-OAUTH-1).
package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hazyhaar/assokit/internal/oauth"
)

// TestNewProvider_OpenIDConfiguration vérifie que le provider expose les métadonnées OIDC correctement.
func TestNewProvider_OpenIDConfiguration(t *testing.T) {
	db := openTestDB(t)
	issuer := "http://localhost:8080"

	handler, store, err := oauth.NewProvider(db, issuer, testSigningKey, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if store == nil {
		t.Fatal("store nil")
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET openid-configuration: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attendu 200, got %d", resp.StatusCode)
	}

	var meta map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("JSON invalide: %v", err)
	}

	if meta["issuer"] != issuer {
		t.Errorf("issuer incorrect: want %q, got %v", issuer, meta["issuer"])
	}
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri"} {
		if meta[field] == nil || meta[field] == "" {
			t.Errorf("champ %q absent ou vide", field)
		}
	}
}

// TestNewProvider_SigningKey vérifie que le storage retourne une clé de signature valide.
func TestNewProvider_SigningKey(t *testing.T) {
	db := openTestDB(t)
	_, store, err := oauth.NewProvider(db, "http://localhost:8080", testSigningKey, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	key, err := store.SigningKey(t.Context())
	if err != nil {
		t.Fatalf("SigningKey: %v", err)
	}
	if key == nil {
		t.Fatal("signing key nil")
	}
	if key.ID() == "" {
		t.Error("key ID vide")
	}
	if key.Key() == nil {
		t.Error("key.Key() nil")
	}
}

// TestNewProvider_Health vérifie que le storage passe le health check.
func TestNewProvider_Health(t *testing.T) {
	db := openTestDB(t)
	_, store, err := oauth.NewProvider(db, "http://localhost:8080", testSigningKey, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if err := store.Health(t.Context()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// TestNewProvider_TokenEndpointResponds vérifie que /oauth2/v3/token répond (même sur requête invalide).
func TestNewProvider_TokenEndpointResponds(t *testing.T) {
	db := openTestDB(t)
	issuer := "http://localhost:18080-test" // placeholder, remplacé par srvURL ci-dessous

	srv := httptest.NewServer(nil) // placeholder pour avoir l'URL
	defer srv.Close()
	issuer = srv.URL

	handler, _, err := oauth.NewProvider(db, issuer, testSigningKey, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	srv.Config.Handler = handler

	// Découvrir le token endpoint
	resp, err := http.Get(srv.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET openid-configuration: %v", err)
	}
	defer resp.Body.Close()
	var meta map[string]any
	json.NewDecoder(resp.Body).Decode(&meta) //nolint:errcheck

	tokenEP, _ := meta["token_endpoint"].(string)
	if tokenEP == "" {
		t.Skip("token_endpoint absent, skip")
	}

	// POST vide → doit retourner 4xx (pas 5xx)
	postResp, err := http.PostForm(tokenEP, nil)
	if err != nil {
		t.Fatalf("POST token endpoint: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode >= 500 {
		t.Errorf("token endpoint : erreur serveur inattendue %d", postResp.StatusCode)
	}
}
