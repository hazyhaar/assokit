// CLAUDE:SUMMARY Tests d'amorçage OAuth des comptes de publication (MountSocialConnect)
// contre un PROVIDER FAKE injecté dans le connecteur X réel (PKCE) : initiation → 302
// vers AuthorizeURL avec state ; rappel valide → jeton persisté au coffre ; rappel à
// l'état invalide/absent → rejet 4xx sans échange.
// CLAUDE:WARN Aucun appel LIVE : l'endpoint de jeton du connecteur est un httptest. Le
// coffre est sur une DB mémoire. La garde requireAdmin est satisfaite par un middleware
// de test qui injecte un utilisateur admin dans le contexte.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	socialx "github.com/hazyhaar/assokit/pkg/connectors/social/x"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"

	_ "modernc.org/sqlite"
)

const testMasterKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// newConnectVault construit un Vault mémoire avec les secrets d'application X posés.
func newConnectVault(t *testing.T) *assets.Vault {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connector_credentials (
			connector_id TEXT NOT NULL, key_name TEXT NOT NULL,
			encrypted_value BLOB NOT NULL,
			set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			set_by TEXT, rotated_at TEXT,
			PRIMARY KEY(connector_id, key_name)
		);`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	v, err := assets.NewVault(db, testMasterKeyHex)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	for k, val := range map[string]string{
		socialx.KeyClientID:     "APP_CLIENT_ID",
		socialx.KeyClientSecret: "APP_CLIENT_SECRET",
	} {
		if err := v.Set(context.Background(), socialx.VaultConnectorID, k, val, ""); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}
	return v
}

// adminInjector est un middleware de test qui place un utilisateur admin dans le
// contexte, satisfaisant la garde requireAdmin sans pile d'authentification réelle.
func adminInjector(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.ContextWithUser(r.Context(), &identity.User{ID: "admin-1", Roles: []string{"admin"}})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// connectTestSetup monte un connecteur X réel sur un endpoint de jeton FAKE, câble
// MountSocialConnect derrière l'injecteur admin et démarre un serveur de test. Le
// vault est retourné pour relire le jeton persisté.
func connectTestSetup(t *testing.T) (srv *httptest.Server, vault *assets.Vault, client *http.Client) {
	t.Helper()

	// Provider FAKE : endpoint de jeton OAuth2 de X (POST .../2/oauth2/token).
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ACCESS_" + form.Get("grant_type"),
			"refresh_token": "REFRESH_" + form.Get("grant_type"),
			"token_type":    "bearer",
			"expires_in":    7200,
		})
	}))
	t.Cleanup(provider.Close)

	vault = newConnectVault(t)
	conn, err := socialx.New(vault, socialx.Config{
		APIBase:     provider.URL,
		RedirectURI: "http://app.test/connect/x/callback",
	}, provider.Client())
	if err != nil {
		t.Fatalf("socialx.New: %v", err)
	}

	r := chi.NewRouter()
	r.Use(adminInjector)
	MountSocialConnect(r, []byte("0123456789abcdef0123456789abcdef"), false, nil, SocialConnector{
		Network:   socialx.NetworkID,
		UsesPKCE:  true,
		Authorize: conn.AuthorizeURL,
		Exchange:  conn.ExchangeCode,
	})

	srv = httptest.NewServer(r)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client = &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // ne pas suivre la redirection vers x.com
		},
	}
	return srv, vault, client
}

// TestSocialConnect_StartRedirectsWithState vérifie l'initiation : 302 vers
// l'AuthorizeURL du fournisseur portant le paramètre state, et dépôt d'un cookie d'état.
func TestSocialConnect_StartRedirectsWithState(t *testing.T) {
	srv, _, client := connectTestSetup(t)

	resp, err := client.Get(srv.URL + "/connect/x/start")
	if err != nil {
		t.Fatalf("GET start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, attendu 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location invalide: %v", err)
	}
	if loc.Host != "x.com" {
		t.Errorf("Location host = %q, attendu x.com", loc.Host)
	}
	if loc.Query().Get("state") == "" {
		t.Error("state absent de l'URL d'autorisation")
	}
	if loc.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, attendu S256", loc.Query().Get("code_challenge_method"))
	}
	var hasStateCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == socialStateCookiePrefix+"x" {
			hasStateCookie = true
		}
	}
	if !hasStateCookie {
		t.Error("cookie d'état non posé")
	}
}

// TestSocialConnect_CallbackPersistsToken vérifie le flux complet : initiation puis
// rappel avec le state concordant et un code factice → le connecteur échange le code
// auprès du provider FAKE et persiste le jeton au coffre.
func TestSocialConnect_CallbackPersistsToken(t *testing.T) {
	srv, vault, client := connectTestSetup(t)

	startResp, err := client.Get(srv.URL + "/connect/x/start")
	if err != nil {
		t.Fatalf("GET start: %v", err)
	}
	startResp.Body.Close()
	loc, _ := url.Parse(startResp.Header.Get("Location"))
	state := loc.Query().Get("state")

	cbResp, err := client.Get(srv.URL + "/connect/x/callback?state=" + url.QueryEscape(state) + "&code=AUTH_CODE_123")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(cbResp.Body)
		t.Fatalf("callback status = %d, attendu 200 (%s)", cbResp.StatusCode, body)
	}

	// Le jeton d'accès a été persisté au coffre par ExchangeCode.
	var got string
	if err := vault.Use(context.Background(), socialx.VaultConnectorID, socialx.KeyAccessToken, func(pt string) error {
		got = pt
		return nil
	}); err != nil {
		t.Fatalf("lecture coffre: %v", err)
	}
	if got != "ACCESS_authorization_code" {
		t.Errorf("jeton au coffre = %q, attendu ACCESS_authorization_code", got)
	}
}

// TestSocialConnect_CallbackRejectsBadState vérifie le rejet anti-CSRF : un rappel sans
// cookie d'état, ou avec un state non concordant, est refusé en 4xx et n'écrit AUCUN
// jeton au coffre (aucun échange n'a lieu).
func TestSocialConnect_CallbackRejectsBadState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withStart bool
		query     string
	}{
		{name: "cookie absent", withStart: false, query: "?state=anything&code=AUTH_CODE"},
		{name: "state non concordant", withStart: true, query: "?state=WRONG_STATE&code=AUTH_CODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, vault, client := connectTestSetup(t)
			if tc.withStart {
				resp, err := client.Get(srv.URL + "/connect/x/start")
				if err != nil {
					t.Fatalf("GET start: %v", err)
				}
				resp.Body.Close()
			}

			cbResp, err := client.Get(srv.URL + "/connect/x/callback" + tc.query)
			if err != nil {
				t.Fatalf("GET callback: %v", err)
			}
			defer cbResp.Body.Close()
			if cbResp.StatusCode < 400 || cbResp.StatusCode >= 500 {
				t.Fatalf("status = %d, attendu 4xx", cbResp.StatusCode)
			}

			// Aucun jeton d'accès ne doit avoir été écrit (l'échange n'a pas eu lieu).
			keys, err := vault.List(context.Background(), socialx.VaultConnectorID)
			if err != nil {
				t.Fatalf("List coffre: %v", err)
			}
			for _, k := range keys {
				if k == socialx.KeyAccessToken {
					t.Errorf("jeton d'accès écrit malgré un état invalide")
				}
			}
		})
	}
}
