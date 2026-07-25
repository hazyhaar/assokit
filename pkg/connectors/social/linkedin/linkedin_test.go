// CLAUDE:SUMMARY Tests de CONTRAT du connecteur LinkedIn — assertion du POST /rest/posts
// (corps JSON author URN + commentary, en-têtes Authorization Bearer + LinkedIn-Version +
// X-Restli-Protocol-Version), du flux d'enregistrement d'asset image (initializeUpload →
// PUT des octets → content.media référençant l'URN), et du flux OAuth2 3-legged (échange
// de code + rafraîchissement, rotation des jetons au coffre).
// CLAUDE:WARN AUCUN appel LIVE : la base API et les endpoints OAuth2 sont injectés sur
// httptest.NewServer. Les serveurs de test sont des doubles de contrat, pas des données
// mock applicatives ; les fixtures média sont des fichiers réels sur disque temporaire.
package linkedin

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"

	_ "modernc.org/sqlite"
)

const testMasterKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

const testAuthorURN = "urn:li:organization:123456"

// newTestVault construit un Vault sur une DB mémoire avec les secrets LinkedIn posés.
func newTestVault(t *testing.T, secrets map[string]string) *assets.Vault {
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
	for k, val := range secrets {
		if err := v.Set(context.Background(), VaultConnectorID, k, val, ""); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}
	return v
}

// TestPublish_TextOnly vérifie le contrat POST /rest/posts sans média : corps JSON
// {author, commentary}, en-têtes Authorization Bearer + LinkedIn-Version +
// X-Restli-Protocol-Version, pas de content.media, et l'id retourné par x-restli-id.
func TestPublish_TextOnly(t *testing.T) {
	var gotPath, gotAuth, gotVersion, gotRestli string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("LinkedIn-Version")
		gotRestli = r.Header.Get("X-Restli-Protocol-Version")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("x-restli-id", "urn:li:share:7180000000000000001")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{APIBase: srv.URL, AuthorURN: testAuthorURN}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:  "Assemblée générale le 12 juin",
		Networks: []string{NetworkID},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if gotPath != "/rest/posts" {
		t.Errorf("path = %q, attendu /rest/posts", gotPath)
	}
	if gotAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization = %q, attendu Bearer USER_ACCESS_TOKEN", gotAuth)
	}
	if gotVersion != DefaultVersion {
		t.Errorf("LinkedIn-Version = %q, attendu %q (défaut)", gotVersion, DefaultVersion)
	}
	if gotRestli != "2.0.0" {
		t.Errorf("X-Restli-Protocol-Version = %q, attendu 2.0.0", gotRestli)
	}
	if gotBody["author"] != testAuthorURN {
		t.Errorf("corps author = %v, attendu %q", gotBody["author"], testAuthorURN)
	}
	if gotBody["commentary"] != "Assemblée générale le 12 juin" {
		t.Errorf("corps commentary = %v", gotBody["commentary"])
	}
	if _, hasContent := gotBody["content"]; hasContent {
		t.Errorf("aucun content attendu en texte seul, corps = %v", gotBody)
	}
	if ref.ExternalID != "urn:li:share:7180000000000000001" || ref.Network != NetworkID {
		t.Errorf("PostRef = %+v", ref)
	}
	if ref.CostCents != 0 {
		t.Errorf("CostCents = %d, attendu 0 (LinkedIn ne facture pas d'API)", ref.CostCents)
	}
}

// TestPublish_VersionInjection vérifie que Config.Version se propage à l'en-tête
// LinkedIn-Version de la requête, et qu'à vide le défaut DefaultVersion s'applique.
func TestPublish_VersionInjection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfgVer  string
		wantVer string
	}{
		{"injectée", "202506", "202506"},
		{"défaut à vide", "", DefaultVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotVersion string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotVersion = r.Header.Get("LinkedIn-Version")
				w.Header().Set("x-restli-id", "urn:li:share:7180000000000000003")
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
			c, err := New(vault, Config{APIBase: srv.URL, AuthorURN: testAuthorURN, Version: tc.cfgVer}, srv.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := c.Publish(context.Background(), social.SocialPost{Content: "x", Networks: []string{NetworkID}}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if gotVersion != tc.wantVer {
				t.Errorf("LinkedIn-Version = %q, attendu %q", gotVersion, tc.wantVer)
			}
		})
	}
}

// TestPublish_WithImage_AssetFlow vérifie le flux d'enregistrement d'asset image
// (initializeUpload → PUT des octets) puis le POST /rest/posts référençant l'URN d'image
// obtenu dans content.media.
func TestPublish_WithImage_AssetFlow(t *testing.T) {
	var steps []string // séquence des appels observés
	var uploadedBytes []byte
	var postContentMediaID string
	var initOwner string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/images" && r.URL.Query().Get("action") == "initializeUpload":
			steps = append(steps, "init")
			body, _ := io.ReadAll(r.Body)
			var parsed imageInitReq
			_ = json.Unmarshal(body, &parsed)
			initOwner = parsed.InitializeUploadRequest.Owner
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": map[string]string{
					"uploadUrl": srv0URL(r) + "/upload/abc",
					"image":     "urn:li:image:C5000-IMG-789",
				},
			})
		case r.URL.Path == "/upload/abc":
			steps = append(steps, "upload")
			uploadedBytes, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/rest/posts":
			steps = append(steps, "post")
			body, _ := io.ReadAll(r.Body)
			var parsed struct {
				Content struct {
					Media struct {
						ID string `json:"id"`
					} `json:"media"`
				} `json:"content"`
			}
			_ = json.Unmarshal(body, &parsed)
			postContentMediaID = parsed.Content.Media.ID
			w.Header().Set("x-restli-id", "urn:li:share:7180000000000000002")
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "endpoint inattendu: "+r.URL.String(), http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Image locale réelle (fixture sur disque, pas une donnée mock applicative).
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "affiche.png")
	imgData := []byte("\x89PNG\r\n\x1a\n octets d'affiche pour le contrat d'asset")
	if err := os.WriteFile(imgPath, imgData, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{APIBase: srv.URL, AuthorURN: testAuthorURN}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Affiche du festival",
		MediaPaths: []string{imgPath},
		Networks:   []string{NetworkID},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if strings.Join(steps, ",") != "init,upload,post" {
		t.Errorf("séquence = %v, attendu [init upload post]", steps)
	}
	if initOwner != testAuthorURN {
		t.Errorf("initializeUpload owner = %q, attendu %q", initOwner, testAuthorURN)
	}
	if string(uploadedBytes) != string(imgData) {
		t.Errorf("octets téléversés ≠ octets de la fixture")
	}
	if postContentMediaID != "urn:li:image:C5000-IMG-789" {
		t.Errorf("content.media.id = %q, attendu l'URN d'image enregistré", postContentMediaID)
	}
	if ref.ExternalID != "urn:li:share:7180000000000000002" {
		t.Errorf("PostRef.ExternalID = %q", ref.ExternalID)
	}
}

// srv0URL reconstruit l'origine (scheme://host) du serveur de test à partir de la requête
// reçue, pour fabriquer une uploadUrl pointant le même httptest (aucun appel LIVE).
func srv0URL(r *http.Request) string {
	return "http://" + r.Host
}

// TestPublish_NoAuthor vérifie le fail-loud quand aucun AuthorURN n'est configuré.
func TestPublish_NoAuthor(t *testing.T) {
	vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
	c, _ := New(vault, Config{APIBase: "http://unused", AuthorURN: ""}, nil)
	if _, err := c.Publish(context.Background(), social.SocialPost{Content: "x", Networks: []string{NetworkID}}); err == nil {
		t.Error("Publish sans AuthorURN aurait dû échouer")
	}
}

// TestOAuth_ExchangeAndRefresh vérifie le flux OAuth2 3-legged : URL d'autorisation
// (scopes), échange du code (authorization_code, client_id/secret au corps) puis
// rafraîchissement (refresh_token), avec rotation des jetons au coffre.
func TestOAuth_ExchangeAndRefresh(t *testing.T) {
	var lastForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastForm, _ = url.ParseQuery(string(body))
		grant := lastForm.Get("grant_type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ACCESS_" + grant,
			"refresh_token": "REFRESH_" + grant,
			"token_type":    "Bearer",
			"expires_in":    5184000,
			"scope":         strings.Join(DefaultScopes, " "),
		})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{
		KeyClientID:     "APP_CLIENT_ID",
		KeyClientSecret: "APP_CLIENT_SECRET",
	})
	c, err := New(vault, Config{
		TokenURL:     srv.URL,
		AuthorizeURL: "https://www.linkedin.com/oauth/v2/authorization",
		RedirectURI:  "https://assokit.test/oauth/linkedin/callback",
	}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// URL d'autorisation : scopes, response_type, client_id, redirect.
	authURL, err := c.AuthorizeURL(context.Background(), "state-xyz")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "APP_CLIENT_ID" {
		t.Errorf("paramètres d'autorisation = %v", q)
	}
	if !strings.Contains(q.Get("scope"), "w_organization_social") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("state") != "state-xyz" {
		t.Errorf("state = %q", q.Get("state"))
	}

	// Échange du code : client_id/secret dans le corps form-urlencodé (pas de Basic auth).
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
	if lastForm.Get("client_id") != "APP_CLIENT_ID" || lastForm.Get("client_secret") != "APP_CLIENT_SECRET" {
		t.Errorf("identifiants client absents du corps: %v", lastForm)
	}

	// Le jeton d'accès a été rotationné au coffre : accessToken() le retourne.
	tok, err := c.accessToken(context.Background())
	if err != nil {
		t.Fatalf("accessToken: %v", err)
	}
	if tok != "ACCESS_authorization_code" {
		t.Errorf("jeton au coffre = %q", tok)
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

// TestNew_FailLoud vérifie le fail-loud sur Vault nil.
func TestNew_FailLoud(t *testing.T) {
	if _, err := New(nil, Config{}, nil); err == nil {
		t.Error("New(nil vault) aurait dû échouer")
	}
}
