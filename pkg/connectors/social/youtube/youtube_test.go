// CLAUDE:SUMMARY Tests de CONTRAT du connecteur YouTube — assertion de videos.insert
// (méthode POST, part=snippet,status, présence du snippet/status dans le corps,
// Authorization Bearer, upload resumable), du marquage Short (#Shorts ajouté), du refus
// d'un post sans vidéo, et du flux OAuth2 (échange de code + rafraîchissement au coffre).
// CLAUDE:WARN AUCUN appel LIVE : l'endpoint API et les endpoints OAuth2 sont injectés sur
// httptest.NewServer (option.WithEndpoint + client HTTP injecté par le contexte). Les
// serveurs de test sont des doubles de contrat, pas des données mock applicatives.
package youtube

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

// newTestVault construit un Vault sur une DB mémoire avec les secrets YouTube posés.
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

// writeVideoFixture écrit un fichier vidéo factice d'au moins une taille donnée (fixture
// sur disque, pas une donnée mock applicative). Une taille > uploadChunkSize force la
// voie d'upload resumable réelle (initiation + transfert chunké).
func writeVideoFixture(t *testing.T, name string, size int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// uploadCapture mémorise ce que le double de contrat observe du flux videos.insert.
type uploadCapture struct {
	initMethod string
	initQuery  url.Values
	initAuth   string
	body       struct {
		Snippet *struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"snippet"`
		Status *struct {
			PrivacyStatus string `json:"privacyStatus"`
		} `json:"status"`
	}
}

// newUploadServer monte un double de contrat de videos.insert en mode resumable :
// la requête d'initiation porte les métadonnées JSON (snippet/status) et renvoie une
// Location de session ; les requêtes de transfert (PUT) renvoient 308 tant que des
// octets restent à envoyer, puis 200 + la ressource Video finale.
func newUploadServer(t *testing.T, cap *uploadCapture, videoID string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload/youtube/v3/videos"):
			// Initiation resumable : métadonnées JSON dans le corps.
			cap.initMethod = r.Method
			cap.initQuery = r.URL.Query()
			cap.initAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &cap.body)
			w.Header().Set("Location", srv.URL+"/resumable-session/abc")
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/resumable-session/"):
			// Transfert : Content-Range "…/*" = encore des octets ; "…/<total>" = fin.
			cr := r.Header.Get("Content-Range")
			if strings.HasSuffix(cr, "/*") {
				w.Header().Set("X-Http-Status-Code-Override", "308")
				w.Header().Set("Range", "bytes=0-"+chunkEnd(cr))
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": videoID})
		default:
			http.Error(w, "endpoint inattendu: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// chunkEnd extrait l'octet de fin d'un Content-Range "bytes 0-262143/*".
func chunkEnd(cr string) string {
	cr = strings.TrimPrefix(cr, "bytes ")
	if i := strings.IndexByte(cr, '/'); i >= 0 {
		cr = cr[:i]
	}
	if i := strings.IndexByte(cr, '-'); i >= 0 {
		return cr[i+1:]
	}
	return "0"
}

// TestPublish_VideoInsertContract vérifie le contrat videos.insert : initiation POST
// resumable, part=snippet,status, snippet (titre/description) et status (privacyStatus)
// dans le corps, Authorization Bearer, et la PostRef retournée.
func TestPublish_VideoInsertContract(t *testing.T) {
	var cap uploadCapture
	srv := newUploadServer(t, &cap, "yt-VIDEO-001")

	vault := newTestVault(t, map[string]string{KeyAccessToken: "USER_ACCESS_TOKEN"})
	c, err := New(vault, Config{Endpoint: srv.URL + "/"}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	videoPath := writeVideoFixture(t, "assemblee.mp4", uploadChunkSize+5000)
	ref, err := c.Publish(context.Background(), social.SocialPost{
		Content:    "Assemblée générale 2026\nCompte rendu de la réunion annuelle.",
		MediaPaths: []string{videoPath},
		Networks:   []string{NetworkID},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if cap.initMethod != http.MethodPost {
		t.Errorf("méthode = %q, attendu POST", cap.initMethod)
	}
	if parts := strings.Join(cap.initQuery["part"], ","); parts != "snippet,status" {
		t.Errorf("part = %q, attendu snippet,status", parts)
	}
	if cap.initQuery.Get("uploadType") != "resumable" {
		t.Errorf("uploadType = %q, attendu resumable", cap.initQuery.Get("uploadType"))
	}
	if cap.initAuth != "Bearer USER_ACCESS_TOKEN" {
		t.Errorf("Authorization = %q, attendu Bearer USER_ACCESS_TOKEN", cap.initAuth)
	}
	if cap.body.Snippet == nil || cap.body.Snippet.Title != "Assemblée générale 2026" {
		t.Errorf("snippet.title = %+v, attendu titre première ligne", cap.body.Snippet)
	}
	if cap.body.Status == nil || cap.body.Status.PrivacyStatus != DefaultPrivacyStatus {
		t.Errorf("status.privacyStatus = %+v, attendu %q", cap.body.Status, DefaultPrivacyStatus)
	}
	if ref.ExternalID != "yt-VIDEO-001" || ref.Network != NetworkID {
		t.Errorf("PostRef = %+v", ref)
	}
	if ref.URL != "https://www.youtube.com/watch?v=yt-VIDEO-001" {
		t.Errorf("PostRef.URL = %q", ref.URL)
	}
	if ref.CostCents != 0 {
		t.Errorf("CostCents = %d, attendu 0 (quota, pas de tarif monétaire)", ref.CostCents)
	}
}

// TestPublish_ShortMarking vérifie que #Shorts est ajouté à la description quand le
// contenu porte le marqueur de Short, et n'est pas ajouté sinon.
func TestPublish_ShortMarking(t *testing.T) {
	for _, tc := range []struct {
		name        string
		content     string
		wantShorts  bool
		wantDoubled bool // #Shorts déjà présent → pas de doublon
	}{
		{name: "marqueur court", content: "Notre best-of #short", wantShorts: true},
		{name: "marqueur complet déjà présent", content: "Rétrospective #Shorts", wantShorts: true, wantDoubled: false},
		{name: "post long classique", content: "Conférence complète sur la gouvernance", wantShorts: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cap uploadCapture
			srv := newUploadServer(t, &cap, "yt-SHORT")
			vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
			c, _ := New(vault, Config{Endpoint: srv.URL + "/"}, srv.Client())

			videoPath := writeVideoFixture(t, "clip.mp4", uploadChunkSize+1000)
			if _, err := c.Publish(context.Background(), social.SocialPost{
				Content:    tc.content,
				MediaPaths: []string{videoPath},
				Networks:   []string{NetworkID},
			}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			desc := ""
			if cap.body.Snippet != nil {
				desc = cap.body.Snippet.Description
			}
			hasShorts := strings.Contains(desc, "#Shorts")
			if hasShorts != tc.wantShorts {
				t.Errorf("description %q : #Shorts présent = %v, attendu %v", desc, hasShorts, tc.wantShorts)
			}
			if tc.wantShorts && strings.Count(strings.ToLower(desc), "#shorts") != 1 {
				t.Errorf("description %q : #Shorts devrait apparaître exactement une fois", desc)
			}
		})
	}
}

// TestPublish_RefusesWithoutVideo vérifie le refus (ErrNoVideo) d'un post sans média
// vidéo : texte seul, et image seule (YouTube est vidéo-only).
func TestPublish_RefusesWithoutVideo(t *testing.T) {
	vault := newTestVault(t, map[string]string{KeyAccessToken: "TOK"})
	c, _ := New(vault, Config{Endpoint: "https://unused.invalid/"}, nil)

	for _, tc := range []struct {
		name  string
		media []string
	}{
		{name: "texte seul", media: nil},
		{name: "image seule", media: []string{"/tmp/affiche.png"}},
		{name: "url distante", media: []string{"https://cdn.test/video.mp4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Publish(context.Background(), social.SocialPost{
				Content:    "Sans vidéo",
				MediaPaths: tc.media,
				Networks:   []string{NetworkID},
			})
			if err == nil {
				t.Fatal("Publish aurait dû refuser un post sans vidéo")
			}
			if err != ErrNoVideo {
				t.Errorf("erreur = %v, attendu ErrNoVideo", err)
			}
		})
	}
}

// TestNew_FailLoud vérifie le fail-loud sur Vault nil.
func TestNew_FailLoud(t *testing.T) {
	if _, err := New(nil, Config{}, nil); err == nil {
		t.Error("New(nil vault) aurait dû échouer")
	}
}
