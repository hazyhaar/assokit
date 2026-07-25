// CLAUDE:SUMMARY Connecteur réseau X (Twitter) — implémente social.Network. Publie via
// l'API v2 (POST /2/tweets, client gotwi) ; média uploadé en v1 chunké (INIT/APPEND/
// FINALIZE) en HTTP direct stdlib. Coût par publication journalisé selon présence d'URL.
// CLAUDE:WARN Aucun appel LIVE hors runtime câblé : la racine API est INJECTABLE
// (Config.APIBase / UploadBase → httptest en test). gotwi cible une URL en dur
// (api.twitter.com) : un RoundTripper de réécriture d'hôte la redirige vers la base
// résolue. Secrets lus au coffre sous "social/x" (clé SANS segment d'instance), jamais
// en dur ni via os.Getenv dans ce package (lecture d'environnement = bordure cmd).
package x

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/michimani/gotwi"
	"github.com/michimani/gotwi/tweet/managetweet"
	managetweettypes "github.com/michimani/gotwi/tweet/managetweet/types"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

const (
	// NetworkID est l'identifiant stable du réseau (le worker route par cette clé).
	NetworkID = "x"

	// DefaultAPIBase est la racine de l'API v2 de X (production). Injectable via
	// Config.APIBase pour pointer un httptest en test (aucun appel LIVE).
	DefaultAPIBase = "https://api.twitter.com"
	// DefaultUploadBase est la racine de l'API d'upload média v1.1. Injectable via
	// Config.UploadBase en test (mêmes serveur que l'API v2, routage par chemin).
	DefaultUploadBase = "https://upload.twitter.com"

	// VaultConnectorID est l'id de coffre des secrets X. Sans segment d'instance
	// (le core est tenant-agnostic : une instance = un coffre).
	VaultConnectorID = "social/x"
	// KeyAccessToken est la clé de coffre du jeton d'accès OAuth2 (Bearer v2).
	KeyAccessToken = "access_token"

	// uploadPath est le chemin de l'endpoint d'upload média v1.1.
	uploadPath = "/1.1/media/upload.json"
	// uploadChunkBytes est la taille d'un segment APPEND (X tolère jusqu'à 5 Mio).
	uploadChunkBytes = 5 * 1024 * 1024

	// CostUSDNoURL et CostUSDWithURL sont les tarifs facturés par X à la publication.
	// X surfacture une publication contenant un lien (les liens externes ont une
	// économie de modération différente). Sources : grille d'usage X API (post simple
	// ~0,015 USD ; post avec lien ~0,20 USD).
	CostUSDNoURL   = 0.015
	CostUSDWithURL = 0.20
)

// Config porte les paramètres NON secrets du connecteur X. Injectée à la bordure
// (cmd), jamais lue dans l'environnement par ce package.
type Config struct {
	// APIBase surcharge la racine API v2 (test → httptest). Vide → DefaultAPIBase.
	APIBase string
	// UploadBase surcharge la racine d'upload média v1.1. Vide → DefaultUploadBase.
	UploadBase string
	// RedirectURI est l'URI de redirection OAuth2 (callback). Requis pour le flux
	// d'autorisation, non secret. Optionnel pour la seule publication.
	RedirectURI string
}

// Connector implémente social.Network pour X (Twitter).
type Connector struct {
	vault      *assets.Vault
	apiBase    string // racine API v2 résolue
	uploadBase string // racine upload v1.1 résolue
	redirect   string
	httpCli    *http.Client // client brut (token + upload média, URL construite ici)
}

// New construit un connecteur X. Fail-loud sur Vault nil : un câblage incomplet ne
// doit jamais démarrer silencieusement. httpCli nil → http.DefaultClient.
func New(vault *assets.Vault, cfg Config, httpCli *http.Client) (*Connector, error) {
	if vault == nil {
		return nil, fmt.Errorf("x.New: Vault requis pour le jeton d'accès")
	}
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	uploadBase := cfg.UploadBase
	if uploadBase == "" {
		uploadBase = DefaultUploadBase
	}
	if httpCli == nil {
		httpCli = http.DefaultClient
	}
	return &Connector{
		vault:      vault,
		apiBase:    strings.TrimRight(apiBase, "/"),
		uploadBase: strings.TrimRight(uploadBase, "/"),
		redirect:   cfg.RedirectURI,
		httpCli:    httpCli,
	}, nil
}

// ID retourne l'identifiant de réseau (le worker route par cette clé).
func (c *Connector) ID() string { return NetworkID }

// Publish diffuse le post sur X et retourne sa référence native. Si un média est
// présent, il est d'abord uploadé en v1 chunké (INIT/APPEND/FINALIZE) pour obtenir un
// media_id, puis le tweet est créé via POST /2/tweets (texte + media_ids). Sinon le
// tweet est créé en texte seul. Le coût est journalisé selon la présence d'une URL
// dans le contenu (cf. PostRef.CostCents, écrit au journal social_post_log par le worker).
func (c *Connector) Publish(ctx context.Context, p social.SocialPost) (social.PostRef, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return social.PostRef{}, err
	}

	var mediaIDs []string
	if path, ok := primaryMedia(p.MediaPaths); ok {
		mediaID, err := c.uploadMediaChunked(ctx, token, path)
		if err != nil {
			return social.PostRef{}, fmt.Errorf("x.Publish upload média: %w", err)
		}
		mediaIDs = []string{mediaID}
	}

	cli, err := c.tweetClient(token)
	if err != nil {
		return social.PostRef{}, err
	}

	in := &managetweettypes.CreateInput{Text: ptr(p.Content)}
	if len(mediaIDs) > 0 {
		in.Media = &managetweettypes.CreateInputMedia{MediaIDs: mediaIDs}
	}
	out, err := managetweet.Create(ctx, cli, in)
	if err != nil {
		return social.PostRef{}, fmt.Errorf("x.Publish POST /2/tweets: %w", err)
	}
	if out.Data.ID == nil {
		return social.PostRef{}, fmt.Errorf("x.Publish: réponse /2/tweets sans id")
	}

	return social.PostRef{
		Network:    NetworkID,
		ExternalID: *out.Data.ID,
		URL:        "https://x.com/i/web/status/" + *out.Data.ID,
		CostCents:  costCents(p.Content),
	}, nil
}

// tweetClient construit le client gotwi en mode OAuth2 Bearer (jeton utilisateur) et
// l'équipe d'un http.Client dont le transport RÉÉCRIT l'hôte api.twitter.com codé en
// dur par gotwi vers la base résolue (c.apiBase). En production la base est
// api.twitter.com : la réécriture est alors une identité. En test elle redirige vers
// le httptest, garantissant qu'aucun appel n'atteint jamais X.
func (c *Connector) tweetClient(token string) (*gotwi.Client, error) {
	base, err := url.Parse(c.apiBase)
	if err != nil {
		return nil, fmt.Errorf("x.tweetClient base invalide %q: %w", c.apiBase, err)
	}
	inner := c.httpCli.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	rewrite := &http.Client{Transport: &hostRewriteTransport{scheme: base.Scheme, host: base.Host, inner: inner}}
	return gotwi.NewClientWithAccessToken(&gotwi.NewClientWithAccessTokenInput{
		HTTPClient:  rewrite,
		AccessToken: token,
	})
}

// hostRewriteTransport réécrit le schéma et l'hôte de chaque requête vers une base
// fixe, en préservant le chemin. Indispensable car gotwi code l'endpoint /2/tweets en
// dur sur api.twitter.com sans point d'injection de base.
type hostRewriteTransport struct {
	scheme string
	host   string
	inner  http.RoundTripper
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.scheme
	clone.URL.Host = t.host
	clone.Host = t.host
	return t.inner.RoundTrip(clone)
}

// uploadMediaChunked exécute le flux d'upload média v1.1 en trois temps : INIT
// (réserve un media_id pour total_bytes + media_type), APPEND (envoie les segments
// indexés en multipart), FINALIZE (clôt l'upload). Retourne le media_id_string.
// Le contenu est lu depuis le chemin local (os.ReadFile) ; l'authentification réutilise
// le jeton OAuth2 Bearer en en-tête Authorization.
func (c *Connector) uploadMediaChunked(ctx context.Context, token, path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // chemin média fourni par la bordure, pas par l'utilisateur final
	if err != nil {
		return "", fmt.Errorf("lecture média %q: %w", path, err)
	}
	mediaType := mimeTypeForExt(path)

	// INIT
	initForm := url.Values{}
	initForm.Set("command", "INIT")
	initForm.Set("total_bytes", strconv.Itoa(len(data)))
	initForm.Set("media_type", mediaType)
	var initRes struct {
		MediaIDString string `json:"media_id_string"`
	}
	if err := c.uploadForm(ctx, token, initForm, &initRes); err != nil {
		return "", fmt.Errorf("INIT: %w", err)
	}
	if initRes.MediaIDString == "" {
		return "", fmt.Errorf("INIT: réponse sans media_id_string")
	}
	mediaID := initRes.MediaIDString

	// APPEND (segments indexés)
	for i, off := 0, 0; off < len(data); i, off = i+1, off+uploadChunkBytes {
		end := off + uploadChunkBytes
		if end > len(data) {
			end = len(data)
		}
		if err := c.uploadAppend(ctx, token, mediaID, i, data[off:end]); err != nil {
			return "", fmt.Errorf("APPEND segment %d: %w", i, err)
		}
	}

	// FINALIZE
	finForm := url.Values{}
	finForm.Set("command", "FINALIZE")
	finForm.Set("media_id", mediaID)
	if err := c.uploadForm(ctx, token, finForm, nil); err != nil {
		return "", fmt.Errorf("FINALIZE: %w", err)
	}
	return mediaID, nil
}

// uploadForm exécute un POST form-urlencodé vers l'endpoint d'upload média (commandes
// INIT / FINALIZE), avec le jeton en en-tête Authorization Bearer.
func (c *Connector) uploadForm(ctx context.Context, token string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.uploadBase+uploadPath, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	return c.doUpload(req, out)
}

// uploadAppend envoie un segment média en multipart (command=APPEND, media_id,
// segment_index, media=octets), avec le jeton en en-tête Authorization Bearer.
func (c *Connector) uploadAppend(ctx context.Context, token, mediaID string, segIndex int, chunk []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("command", "APPEND")
	_ = mw.WriteField("media_id", mediaID)
	_ = mw.WriteField("segment_index", strconv.Itoa(segIndex))
	part, err := mw.CreateFormFile("media", "blob")
	if err != nil {
		return fmt.Errorf("part media: %w", err)
	}
	if _, err := part.Write(chunk); err != nil {
		return fmt.Errorf("écriture chunk: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("clôture multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.uploadBase+uploadPath, &buf)
	if err != nil {
		return fmt.Errorf("requête: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return c.doUpload(req, nil)
}

// doUpload exécute une requête d'upload et désérialise la réponse JSON dans out
// (si non nil). Une réponse hors 2xx remonte le corps (diagnostic). APPEND répond
// 204 No Content : out nil est alors attendu.
func (c *Connector) doUpload(req *http.Request, out any) error {
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("appel upload: %w", err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("lecture réponse upload: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, strings.TrimSpace(body.String()))
	}
	if out == nil || body.Len() == 0 {
		return nil
	}
	if err := json.Unmarshal(body.Bytes(), out); err != nil {
		return fmt.Errorf("décodage réponse upload: %w", err)
	}
	return nil
}

// accessToken lit le jeton d'accès OAuth2 depuis le coffre. Le plaintext est zéro-out
// par Vault.Use après copie (aucune référence conservée par ce package).
func (c *Connector) accessToken(ctx context.Context) (string, error) {
	var token string
	err := c.vault.Use(ctx, VaultConnectorID, KeyAccessToken, func(plaintext string) error {
		token = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("x.accessToken: %w", err)
	}
	if token == "" {
		return "", fmt.Errorf("x.accessToken: jeton d'accès vide au coffre")
	}
	return token, nil
}

// costCents retourne le coût de publication en centimes USD selon la présence d'une
// URL dans le contenu. Le coût est représenté sur la colonne entière cost_cents du
// journal social_post_log : les tarifs sub-centime (0,015 USD) sont arrondis au
// centime le plus proche (→ 2 ¢), ce qui suffit à différencier les deux régimes
// tarifaires tout en restant dans le type de la colonne existante.
func costCents(content string) int {
	if containsURL(content) {
		return int(math.Round(CostUSDWithURL * 100))
	}
	return int(math.Round(CostUSDNoURL * 100))
}

// containsURL indique si le texte contient au moins un lien http(s).
func containsURL(s string) bool {
	return strings.Contains(s, "http://") || strings.Contains(s, "https://")
}

// videoExtensions liste les extensions traitées comme vidéo (type MIME dédié).
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".avi": true,
}

// primaryMedia retourne le premier média LOCAL (chemin de fichier) et true s'il y en
// a un. Les entrées qui sont des URLs http(s) ne sont pas des fichiers à uploader.
func primaryMedia(paths []string) (string, bool) {
	for _, p := range paths {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			continue
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		return p, true
	}
	return "", false
}

// mimeTypeForExt retourne un type MIME plausible pour l'extension du média, défaut
// application/octet-stream. Sert à renseigner media_type lors de l'INIT v1.1.
func mimeTypeForExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if videoExtensions[ext] {
		switch ext {
		case ".mp4", ".m4v":
			return "video/mp4"
		case ".mov":
			return "video/quicktime"
		case ".webm":
			return "video/webm"
		case ".avi":
			return "video/x-msvideo"
		}
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}

// ptr retourne un pointeur vers v (helper pour les champs optionnels gotwi).
func ptr[T any](v T) *T { return &v }
