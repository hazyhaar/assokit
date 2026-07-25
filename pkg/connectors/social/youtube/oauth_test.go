// CLAUDE:SUMMARY Test de CONTRAT du flux OAuth2 YouTube — URL d'autorisation
// (access_type=offline, scope youtube.upload), échange du code (authorization_code) et
// rafraîchissement (refresh_token) avec rotation des jetons au coffre.
// CLAUDE:WARN AUCUN appel LIVE : l'endpoint de jeton est injecté sur httptest et le
// client HTTP est passé par le contexte oauth2.HTTPClient. Aucune requête n'atteint
// googleapis.com.
package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestOAuth_ExchangeAndRefresh vérifie le flux OAuth2 : URL d'autorisation (offline,
// scopes), échange du code contre access/refresh token, puis rafraîchissement, avec
// rotation des jetons au coffre.
func TestOAuth_ExchangeAndRefresh(t *testing.T) {
	var lastForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastForm, _ = url.ParseQuery(string(body))
		grant := lastForm.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ACCESS_" + grant,
			"refresh_token": "REFRESH_" + grant,
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{
		KeyClientID:     "APP_CLIENT_ID",
		KeyClientSecret: "APP_CLIENT_SECRET",
	})
	c, err := New(vault, Config{
		TokenURL:    srv.URL + "/token",
		AuthURL:     srv.URL + "/auth",
		RedirectURI: "https://assokit.test/oauth/youtube/callback",
	}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// URL d'autorisation : offline + scope youtube.upload.
	authURL, err := c.AuthorizeURL(context.Background(), "state-xyz")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("access_type") != "offline" {
		t.Errorf("access_type = %q, attendu offline (refresh_token requis)", q.Get("access_type"))
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != "APP_CLIENT_ID" {
		t.Errorf("paramètres d'autorisation = %v", q)
	}
	if !strings.Contains(q.Get("scope"), "youtube.upload") {
		t.Errorf("scope = %q, attendu youtube.upload", q.Get("scope"))
	}

	// Échange du code.
	access, err := c.ExchangeCode(context.Background(), "AUTH_CODE_123")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if access != "ACCESS_authorization_code" {
		t.Errorf("access_token échange = %q", access)
	}
	if lastForm.Get("grant_type") != "authorization_code" || lastForm.Get("code") != "AUTH_CODE_123" {
		t.Errorf("form échange = %v", lastForm)
	}

	// Les jetons ont été rotationnés au coffre.
	tokAccess, err := c.vaultGet(context.Background(), KeyAccessToken)
	if err != nil {
		t.Fatalf("lecture access_token au coffre: %v", err)
	}
	if tokAccess != "ACCESS_authorization_code" {
		t.Errorf("access_token au coffre = %q", tokAccess)
	}
	tokRefresh, _ := c.vaultGet(context.Background(), KeyRefreshToken)
	if tokRefresh != "REFRESH_authorization_code" {
		t.Errorf("refresh_token au coffre = %q", tokRefresh)
	}

	// Rafraîchissement : utilise le refresh_token rotationné par l'échange.
	access2, err := c.RefreshToken(context.Background())
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if access2 != "ACCESS_refresh_token" {
		t.Errorf("access_token rafraîchi = %q", access2)
	}
	if lastForm.Get("grant_type") != "refresh_token" || lastForm.Get("refresh_token") != "REFRESH_authorization_code" {
		t.Errorf("form rafraîchissement = %v", lastForm)
	}
}

// TestRefreshToken_NoRefreshAtVault vérifie ErrNoRefreshToken quand aucun refresh_token
// n'est au coffre (flux d'autorisation initial non encore réalisé).
func TestRefreshToken_NoRefreshAtVault(t *testing.T) {
	vault := newTestVault(t, map[string]string{
		KeyClientID:     "ID",
		KeyClientSecret: "SECRET",
	})
	c, _ := New(vault, Config{TokenURL: "https://unused.invalid/token"}, nil)
	if _, err := c.RefreshToken(context.Background()); err != ErrNoRefreshToken {
		t.Errorf("erreur = %v, attendu ErrNoRefreshToken", err)
	}
}
