// CLAUDE:SUMMARY Tests de CONTRAT du connecteur Meta — assertion des requêtes Graph
// (IG container+publish, FB feed) et de l'échange de jeton, contre un httptest.
// CLAUDE:WARN AUCUN appel LIVE : la racine Graph est injectée sur httptest.NewServer.
// Les serveurs de test sont des doubles de contrat, pas des données mock applicatives.
package meta

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"

	_ "modernc.org/sqlite"
)

const testMasterKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// newTestVault construit un Vault sur une DB mémoire avec les secrets Meta posés.
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

// recordedReq capture une requête Graph reçue par le serveur de test.
type recordedReq struct {
	method string
	path   string
	auth   string
	form   url.Values
}

// TestPublishInstagram_ContainerThenPublish vérifie le contrat Graph Instagram :
// création d'un conteneur média (caption + media) puis publication (creation_id),
// avec l'en-tête Authorization Bearer sur chaque appel.
func TestPublishInstagram_ContainerThenPublish(t *testing.T) {
	var got []recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		got = append(got, recordedReq{
			method: r.Method, path: r.URL.Path,
			auth: r.Header.Get("Authorization"), form: form,
		})
		switch {
		case strings.HasSuffix(r.URL.Path, "/media"):
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "container-123"})
		case strings.HasSuffix(r.URL.Path, "/media_publish"):
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "media-456"})
		default:
			http.Error(w, "endpoint inattendu", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyPageAccessToken: "IG_TOKEN"})
	c, err := New(vault, Config{
		Surface:   SurfaceInstagram,
		TargetID:  "17841400000000000",
		GraphBase: srv.URL,
	}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Sortie en mer dimanche",
		MediaPaths: []string{"https://cdn.test/reel.mp4"},
		Networks:   []string{SurfaceInstagram},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("attendu 2 appels Graph (container + publish), obtenu %d", len(got))
	}
	// 1. Conteneur média : POST /v21.0/{ig}/media, REELS + video_url + caption.
	c0 := got[0]
	if c0.method != http.MethodPost || !strings.HasSuffix(c0.path, "/"+DefaultVersion+"/17841400000000000/media") {
		t.Errorf("appel container = %s %s", c0.method, c0.path)
	}
	if c0.auth != "Bearer IG_TOKEN" {
		t.Errorf("Authorization container = %q, attendu Bearer IG_TOKEN", c0.auth)
	}
	if c0.form.Get("media_type") != "REELS" || c0.form.Get("video_url") != "https://cdn.test/reel.mp4" {
		t.Errorf("form container = %v", c0.form)
	}
	if c0.form.Get("caption") != "Sortie en mer dimanche" {
		t.Errorf("caption container = %q", c0.form.Get("caption"))
	}
	// 2. Publication : POST /v21.0/{ig}/media_publish, creation_id = id du conteneur.
	c1 := got[1]
	if c1.method != http.MethodPost || !strings.HasSuffix(c1.path, "/media_publish") {
		t.Errorf("appel publish = %s %s", c1.method, c1.path)
	}
	if c1.form.Get("creation_id") != "container-123" {
		t.Errorf("creation_id = %q, attendu container-123", c1.form.Get("creation_id"))
	}
	if ref.ExternalID != "media-456" || ref.Network != SurfaceInstagram {
		t.Errorf("PostRef = %+v, attendu ExternalID=media-456 Network=instagram", ref)
	}
}

// TestPublishFacebook_FeedMessage vérifie le contrat Graph Facebook pour un post
// texte : POST /v21.0/{page}/feed, message dans le corps, Authorization Bearer.
func TestPublishFacebook_FeedMessage(t *testing.T) {
	var got recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		got = recordedReq{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), form: form}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "page_987_post_654"})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyPageAccessToken: "FB_TOKEN"})
	c, err := New(vault, Config{
		Surface:   SurfaceFacebook,
		TargetID:  "987654321",
		GraphBase: srv.URL,
	}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:  "Assemblée générale le 12 juin",
		Networks: []string{SurfaceFacebook},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got.method != http.MethodPost || !strings.HasSuffix(got.path, "/"+DefaultVersion+"/987654321/feed") {
		t.Errorf("appel feed = %s %s", got.method, got.path)
	}
	if got.auth != "Bearer FB_TOKEN" {
		t.Errorf("Authorization feed = %q, attendu Bearer FB_TOKEN", got.auth)
	}
	if got.form.Get("message") != "Assemblée générale le 12 juin" {
		t.Errorf("message feed = %q", got.form.Get("message"))
	}
	if ref.ExternalID != "page_987_post_654" || ref.Network != SurfaceFacebook {
		t.Errorf("PostRef = %+v", ref)
	}
}

// TestPublishFacebook_VideoEndpoint vérifie qu'une vidéo bascule sur /videos.
func TestPublishFacebook_VideoEndpoint(t *testing.T) {
	var got recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		got = recordedReq{path: r.URL.Path, form: form}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "vid-1"})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{KeyPageAccessToken: "FB_TOKEN"})
	c, _ := New(vault, Config{Surface: SurfaceFacebook, TargetID: "555", GraphBase: srv.URL}, srv.Client())

	if _, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Teaser",
		MediaPaths: []string{"https://cdn.test/clip.mov"},
		Networks:   []string{SurfaceFacebook},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !strings.HasSuffix(got.path, "/555/videos") {
		t.Errorf("attendu /videos pour une vidéo, path = %s", got.path)
	}
	if got.form.Get("file_url") != "https://cdn.test/clip.mov" {
		t.Errorf("file_url = %q", got.form.Get("file_url"))
	}
}

// TestRefreshLongLivedToken vérifie l'échange fb_exchange_token et la rotation au coffre.
func TestRefreshLongLivedToken(t *testing.T) {
	var got recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		got = recordedReq{method: r.Method, path: r.URL.Path, form: form}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "NEW_LONG_LIVED_TOKEN",
			"token_type":   "bearer",
			"expires_in":   5183944,
		})
	}))
	defer srv.Close()

	vault := newTestVault(t, map[string]string{
		KeyPageAccessToken: "OLD_TOKEN",
		KeyAppSecret:       "APP_SECRET",
	})
	c, err := New(vault, Config{
		Surface:   SurfaceFacebook,
		TargetID:  "987",
		AppID:     "111222333",
		GraphBase: srv.URL,
	}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	newTok, err := c.RefreshLongLivedToken(context.Background())
	if err != nil {
		t.Fatalf("RefreshLongLivedToken: %v", err)
	}
	if newTok != "NEW_LONG_LIVED_TOKEN" {
		t.Fatalf("nouveau jeton = %q", newTok)
	}
	if got.method != http.MethodPost || !strings.HasSuffix(got.path, "/oauth/access_token") {
		t.Errorf("appel échange = %s %s", got.method, got.path)
	}
	if got.form.Get("grant_type") != "fb_exchange_token" ||
		got.form.Get("client_id") != "111222333" ||
		got.form.Get("client_secret") != "APP_SECRET" ||
		got.form.Get("fb_exchange_token") != "OLD_TOKEN" {
		t.Errorf("paramètres d'échange = %v", got.form)
	}

	// La rotation a remplacé le jeton au coffre : un accessToken() suivant retourne le neuf.
	tok, err := c.accessToken(context.Background())
	if err != nil {
		t.Fatalf("accessToken: %v", err)
	}
	if tok != "NEW_LONG_LIVED_TOKEN" {
		t.Errorf("jeton au coffre après rotation = %q, attendu NEW_LONG_LIVED_TOKEN", tok)
	}
}

// TestNew_FailLoud vérifie le fail-loud sur configuration incomplète.
func TestNew_FailLoud(t *testing.T) {
	vault := newTestVault(t, nil)
	for _, tc := range []struct {
		name string
		cfg  Config
		v    *assets.Vault
	}{
		{"vault nil", Config{Surface: SurfaceFacebook, TargetID: "1"}, nil},
		{"surface inconnue", Config{Surface: "tiktok", TargetID: "1"}, vault},
		{"target vide", Config{Surface: SurfaceInstagram, TargetID: ""}, vault},
	} {
		if _, err := New(tc.v, tc.cfg, nil); err == nil {
			t.Errorf("%s: New aurait dû échouer", tc.name)
		}
	}
}
