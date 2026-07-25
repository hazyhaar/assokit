// CLAUDE:SUMMARY Tests de CONTRAT du connecteur X — assertion du POST /2/tweets (corps
// JSON text + media.media_ids, Authorization Bearer), du flux d'upload média v1
// (INIT/APPEND/FINALIZE), de la différenciation de coût selon présence d'URL, et du
// flux OAuth2 PKCE (échange de code + rafraîchissement).
// CLAUDE:WARN AUCUN appel LIVE : la base API est injectée sur httptest.NewServer ; le
// transport gotwi est réécrit vers ce serveur. Les serveurs de test sont des doubles
// de contrat, pas des données mock applicatives.
package x

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
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

// newTestVault construit un Vault sur une DB mémoire avec les secrets X posés.
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

// TestPublish_TextOnly vérifie le contrat POST /2/tweets sans média : corps JSON
// {text}, en-tête Authorization Bearer, pas de media_ids.
func TestPublish_TextOnly(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "1850000000000000001", "text": "Assemblée générale le 12 juin"}})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{APIBase: srv.URL}, srv.Client())
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

	if gotPath != "/2/tweets" {
		t.Errorf("path = %q, attendu /2/tweets", gotPath)
	}
	if gotAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization = %q, attendu Bearer USER_ACCESS_TOKEN", gotAuth)
	}
	if gotBody["text"] != "Assemblée générale le 12 juin" {
		t.Errorf("corps text = %v", gotBody["text"])
	}
	if _, hasMedia := gotBody["media"]; hasMedia {
		t.Errorf("aucun media attendu en texte seul, corps = %v", gotBody)
	}
	if ref.ExternalID != "1850000000000000001" || ref.Network != NetworkID {
		t.Errorf("PostRef = %+v", ref)
	}
}

// TestPublish_WithMedia_ChunkedUpload vérifie le flux complet d'upload média v1
// (INIT → APPEND → FINALIZE) puis le POST /2/tweets référençant le media_id obtenu.
func TestPublish_WithMedia_ChunkedUpload(t *testing.T) {
	var commands []string // séquence des commandes d'upload observées
	var tweetMediaIDs []any
	var uploadAuth, tweetAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, uploadPath):
			uploadAuth = r.Header.Get("Authorization")
			cmd := readUploadCommand(t, r)
			commands = append(commands, cmd)
			switch cmd {
			case "INIT":
				_ = json.NewEncoder(w).Encode(map[string]any{"media_id_string": "media-789"})
			case "APPEND":
				w.WriteHeader(http.StatusNoContent)
			case "FINALIZE":
				_ = json.NewEncoder(w).Encode(map[string]any{"media_id_string": "media-789"})
			}
		case r.URL.Path == "/2/tweets":
			tweetAuth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			var parsed struct {
				Media struct {
					MediaIDs []any `json:"media_ids"`
				} `json:"media"`
			}
			_ = json.Unmarshal(body, &parsed)
			tweetMediaIDs = parsed.Media.MediaIDs
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "1850000000000000002"}})
		default:
			http.Error(w, "endpoint inattendu: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Média local réel (fixture sur disque, pas une donnée mock applicative).
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "affiche.png")
	if err := os.WriteFile(mediaPath, []byte("\x89PNG\r\n\x1a\n fake bytes pour le contrat d'upload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{APIBase: srv.URL, UploadBase: srv.URL}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Affiche du festival",
		MediaPaths: []string{mediaPath},
		Networks:   []string{NetworkID},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := []string{"INIT", "APPEND", "FINALIZE"}
	if strings.Join(commands, ",") != strings.Join(want, ",") {
		t.Errorf("séquence d'upload = %v, attendu %v", commands, want)
	}
	if uploadAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization upload = %q", uploadAuth)
	}
	if tweetAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization tweet = %q", tweetAuth)
	}
	if len(tweetMediaIDs) != 1 || tweetMediaIDs[0] != "media-789" {
		t.Errorf("media_ids du tweet = %v, attendu [media-789]", tweetMediaIDs)
	}
	if ref.ExternalID != "1850000000000000002" {
		t.Errorf("PostRef.ExternalID = %q", ref.ExternalID)
	}
}

// readUploadCommand extrait la commande (INIT/APPEND/FINALIZE) d'une requête d'upload,
// que le corps soit form-urlencodé (INIT/FINALIZE) ou multipart (APPEND).
func readUploadCommand(t *testing.T, r *http.Request) string {
	t.Helper()
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		_, params, _ := mime.ParseMediaType(ct)
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		_ = params
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "command" {
				b, _ := io.ReadAll(part)
				return string(b)
			}
		}
		return ""
	}
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	return form.Get("command")
}

// TestPublish_CostDiffersByURL vérifie que le coût journalisé (PostRef.CostCents)
// diffère selon la présence d'une URL dans le contenu.
func TestPublish_CostDiffersByURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "1850000000000000003"}})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
	c, _ := New(vault, Config{APIBase: srv.URL}, srv.Client())

	noURL, err := c.Publish(context.Background(), social.SocialPost{Content: "Rejoignez le club", Networks: []string{NetworkID}})
	if err != nil {
		t.Fatalf("Publish sans URL: %v", err)
	}
	withURL, err := c.Publish(context.Background(), social.SocialPost{Content: "Infos sur https://club.test/ag", Networks: []string{NetworkID}})
	if err != nil {
		t.Fatalf("Publish avec URL: %v", err)
	}

	if noURL.CostCents == withURL.CostCents {
		t.Fatalf("coût identique (%d) alors que la présence d'URL diffère", noURL.CostCents)
	}
	if withURL.CostCents <= noURL.CostCents {
		t.Errorf("un post avec URL (%d ¢) devrait coûter plus qu'un post sans (%d ¢)", withURL.CostCents, noURL.CostCents)
	}
	if noURL.CostCents != 2 || withURL.CostCents != 20 {
		t.Errorf("coûts = sans URL %d ¢ / avec URL %d ¢, attendu 2 / 20", noURL.CostCents, withURL.CostCents)
	}
}

// TestOAuth_ExchangeAndRefresh vérifie le flux OAuth2 PKCE : URL d'autorisation
// (code_challenge S256), échange du code (authorization_code + code_verifier) puis
// rafraîchissement (refresh_token), avec rotation des jetons au coffre.
func TestOAuth_ExchangeAndRefresh(t *testing.T) {
	var lastForm url.Values
	var lastAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuthHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		lastForm, _ = url.ParseQuery(string(body))
		grant := lastForm.Get("grant_type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ACCESS_" + grant,
			"refresh_token": "REFRESH_" + grant,
			"token_type":    "bearer",
			"expires_in":    7200,
			"scope":         strings.Join(DefaultScopes, " "),
		})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{
		KeyClientID:     "APP_CLIENT_ID",
		KeyClientSecret: "APP_CLIENT_SECRET",
	})
	c, err := New(vault, Config{APIBase: srv.URL, RedirectURI: "https://assokit.test/oauth/x/callback"}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// URL d'autorisation : PKCE S256, scopes, redirect.
	verifier, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	authURL, err := c.AuthorizeURL(context.Background(), "state-xyz", verifier)
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != challengeS256(verifier) {
		t.Errorf("code_challenge ne correspond pas au verifier")
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != "APP_CLIENT_ID" {
		t.Errorf("paramètres d'autorisation = %v", q)
	}
	if !strings.Contains(q.Get("scope"), "tweet.write") {
		t.Errorf("scope = %q", q.Get("scope"))
	}

	// Échange du code.
	access, err := c.ExchangeCode(context.Background(), "AUTH_CODE_123", verifier)
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if access != "ACCESS_authorization_code" {
		t.Errorf("access_token échange = %q", access)
	}
	if lastForm.Get("grant_type") != "authorization_code" || lastForm.Get("code") != "AUTH_CODE_123" || lastForm.Get("code_verifier") != verifier {
		t.Errorf("form échange = %v", lastForm)
	}
	if !strings.HasPrefix(lastAuthHeader, "Basic ") {
		t.Errorf("Authorization échange = %q, attendu Basic", lastAuthHeader)
	}
	// Le client confidentiel s'authentifie en Basic client_id:client_secret.
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(lastAuthHeader, "Basic "))
	if !strings.Contains(string(raw), "APP_CLIENT_ID") {
		t.Errorf("Basic auth ne contient pas le client_id")
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
