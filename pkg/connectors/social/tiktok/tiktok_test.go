// CLAUDE:SUMMARY Tests de CONTRAT du connecteur TikTok — assertion de l'init de
// publication (POST /v2/post/publish/inbox/video/init/, corps source_info FILE_UPLOAD,
// Authorization Bearer), du transfert vidéo (PUT vers l'upload_url retourné), du suivi
// de statut (POST /v2/post/publish/status/fetch/), du refus si pas de vidéo, du flux
// OAuth2 PKCE (échange + rafraîchissement, jeton rotationné au coffre), et du fait que
// le PostRef reflète « brouillon / en attente », jamais « publié ».
// CLAUDE:WARN AUCUN appel LIVE : la base API et l'upload_url sont servis par
// httptest.NewServer. Les serveurs de test sont des doubles de contrat, pas des données
// mock applicatives ; la vidéo est une fixture réelle écrite sur disque.
package tiktok

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

// newTestVault construit un Vault sur une DB mémoire avec les secrets TikTok posés.
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

// writeFixtureVideo écrit une fixture vidéo réelle sur disque (pas une donnée mock
// applicative : un fichier local que le connecteur lit et transfère).
func writeFixtureVideo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "annonce.mp4")
	if err := os.WriteFile(path, []byte("\x00\x00\x00\x18ftypmp42 octets de contrat d'upload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestPublish_DraftInInbox vérifie le contrat complet de dépôt d'un brouillon : init
// (endpoint, corps source_info FILE_UPLOAD, Bearer), transfert PUT vers l'upload_url
// retourné, suivi de statut, et PostRef reflétant « brouillon en attente », pas publié.
func TestPublish_DraftInInbox(t *testing.T) {
	var (
		initPathGot, statusPathGot string
		initAuth, statusAuth       string
		initBody                   initReq
		uploadHit                  bool
		uploadRange                string
		statusPublishID            string
	)

	mux := http.NewServeMux()
	mux.HandleFunc(initPath, func(w http.ResponseWriter, r *http.Request) {
		initPathGot = r.URL.Path
		initAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &initBody)
		// upload_url pointe le MÊME httptest (chemin /upload) : aucun octet n'atteint TikTok.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]string{"publish_id": "pub-123", "upload_url": serverURL(r) + "/upload"},
			"error": map[string]string{"code": "ok", "message": ""},
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		uploadHit = true
		uploadRange = r.Header.Get("Content-Range")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, r *http.Request) {
		statusPathGot = r.URL.Path
		statusAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var sr statusReq
		_ = json.Unmarshal(body, &sr)
		statusPublishID = sr.PublishID
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]string{"status": statusInbox},
			"error": map[string]string{"code": "ok"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{APIBase: srv.URL}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	video := writeFixtureVideo(t)
	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Notre vidéo de présentation",
		MediaPaths: []string{video},
		Networks:   []string{NetworkID},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Init de publication.
	if initPathGot != initPath {
		t.Errorf("init path = %q, attendu %q", initPathGot, initPath)
	}
	if initAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization init = %q", initAuth)
	}
	if initBody.SourceInfo.Source != sourceFileUpload {
		t.Errorf("source = %q, attendu %q", initBody.SourceInfo.Source, sourceFileUpload)
	}
	if initBody.SourceInfo.VideoSize <= 0 || initBody.SourceInfo.TotalChunkCount != 1 {
		t.Errorf("source_info incohérent: %+v", initBody.SourceInfo)
	}

	// Transfert vidéo.
	if !uploadHit {
		t.Error("l'upload_url retourné par l'init n'a pas été appelé")
	}
	if !strings.HasPrefix(uploadRange, "bytes 0-") {
		t.Errorf("Content-Range upload = %q", uploadRange)
	}

	// Suivi de statut.
	if statusPathGot != statusPath {
		t.Errorf("status path = %q, attendu %q", statusPathGot, statusPath)
	}
	if statusAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization statut = %q", statusAuth)
	}
	if statusPublishID != "pub-123" {
		t.Errorf("publish_id suivi = %q, attendu pub-123", statusPublishID)
	}

	// PostRef : brouillon en attente, PAS publié.
	if ref.ExternalID != "pub-123" || ref.Network != NetworkID {
		t.Errorf("PostRef = %+v", ref)
	}
	if ref.URL != DraftPending {
		t.Errorf("PostRef.URL = %q, attendu le marqueur de brouillon en attente", ref.URL)
	}
	if strings.HasPrefix(ref.URL, "http") {
		t.Errorf("PostRef.URL ne doit PAS être une URL publique (brouillon non publié), got %q", ref.URL)
	}
}

// TestPublish_RefusSansVideo vérifie le refus si aucun média vidéo n'est fourni
// (TikTok est vidéo-only). Aucun appel réseau ne doit être émis.
func TestPublish_RefusSansVideo(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
	c, _ := New(vault, Config{APIBase: srv.URL}, srv.Client())

	// Texte seul (pas de média).
	if _, err := c.Publish(context.Background(), social.SocialPost{Content: "Texte seul", Networks: []string{NetworkID}}); err == nil {
		t.Error("Publish sans vidéo aurait dû échouer (ErrNoVideo)")
	}
	// Une image n'est pas une vidéo.
	if _, err := c.Publish(context.Background(), social.SocialPost{Content: "Affiche", MediaPaths: []string{"/tmp/affiche.png"}, Networks: []string{NetworkID}}); err == nil {
		t.Error("Publish avec une image aurait dû échouer (TikTok est vidéo-only)")
	}
	if called {
		t.Error("aucun appel réseau ne devrait être émis quand la vidéo manque")
	}
}

// TestPublish_StatutEchec vérifie qu'un statut FAILED remonte une erreur (le brouillon
// n'a pas été accepté), sans prétendre au succès.
func TestPublish_StatutEchec(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(initPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]string{"publish_id": "pub-x", "upload_url": serverURL(r) + "/upload"},
			"error": map[string]string{"code": "ok"},
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]string{"status": statusFailed, "fail_reason": "format non supporté"},
			"error": map[string]string{"code": "ok"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
	c, _ := New(vault, Config{APIBase: srv.URL}, srv.Client())

	if _, err := c.Publish(context.Background(), social.SocialPost{
		Content: "v", MediaPaths: []string{writeFixtureVideo(t)}, Networks: []string{NetworkID},
	}); err == nil {
		t.Error("Publish avec statut FAILED aurait dû échouer")
	}
}

// TestOAuth_ExchangeAndRefresh vérifie le flux OAuth2 PKCE TikTok : URL d'autorisation
// (client_key, scope video.upload, code_challenge S256), échange du code, puis
// rafraîchissement, avec rotation des jetons au coffre. Les identifiants sont dans le
// CORPS (TikTok n'utilise pas Basic auth).
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
			"expires_in":    7200,
			"scope":         strings.Join(DefaultScopes, ","),
			"open_id":       "open-abc",
		})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{
		KeyClientKey:    "APP_CLIENT_KEY",
		KeyClientSecret: "APP_CLIENT_SECRET",
	})
	c, err := New(vault, Config{TokenURL: srv.URL + "/token", AuthorizeURL: srv.URL + "/authorize", RedirectURI: "https://assokit.test/oauth/tiktok/callback"}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// URL d'autorisation : PKCE S256, scope, client_key.
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
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != challengeS256(verifier) {
		t.Errorf("PKCE invalide: method=%q challenge=%q", q.Get("code_challenge_method"), q.Get("code_challenge"))
	}
	if q.Get("client_key") != "APP_CLIENT_KEY" || q.Get("response_type") != "code" {
		t.Errorf("paramètres d'autorisation = %v", q)
	}
	if !strings.Contains(q.Get("scope"), "video.upload") {
		t.Errorf("scope = %q, attendu video.upload", q.Get("scope"))
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
	if lastForm.Get("client_key") != "APP_CLIENT_KEY" || lastForm.Get("client_secret") != "APP_CLIENT_SECRET" {
		t.Errorf("identifiants absents du corps: %v", lastForm)
	}

	// Le jeton d'accès a été rotationné au coffre.
	tok, err := c.accessToken(context.Background())
	if err != nil {
		t.Fatalf("accessToken: %v", err)
	}
	if tok != "ACCESS_authorization_code" {
		t.Errorf("jeton au coffre = %q", tok)
	}

	// Rafraîchissement.
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

// serverURL reconstruit la racine du httptest depuis une requête entrante (schéma + hôte),
// pour fabriquer un upload_url pointant le même serveur de test.
func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
