// CLAUDE:SUMMARY Connecteur réseau TikTok — implémente social.Network. TikTok est
// VIDÉO-ONLY : Publish exige un média vidéo (ErrNoVideo sinon). Diffusion via la
// Content Posting API en HTTP direct stdlib : init de l'upload (POST /v2/post/publish/
// inbox/video/init/), transfert du fichier vers l'upload_url retourné (PUT chunké),
// puis suivi de statut (POST /v2/post/publish/status/fetch/). MODÈLE BROUILLON-EN-BOÎTE :
// l'endpoint inbox DÉPOSE un brouillon dans la boîte de réception du compte, qui exige
// une validation MANUELLE avant publication réelle — le PostRef ne prétend donc JAMAIS
// « publié » (URL publique vide, statut « brouillon soumis, en attente de validation »).
// CLAUDE:WARN Aucun appel LIVE : la racine API est INJECTABLE (Config.APIBase → httptest
// en test) et l'upload cible l'upload_url retourné par l'init (httptest en test). Secrets
// lus au coffre sous "social/tiktok" (clé SANS segment d'instance), jamais en dur ni via
// os.Getenv dans ce package (lecture d'environnement = bordure cmd). Le coût TikTok est
// un quota d'API, pas un tarif monétaire : CostCents reste 0.
package tiktok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

const (
	// NetworkID est l'identifiant stable du réseau (le worker route par cette clé).
	NetworkID = "tiktok"

	// DefaultAPIBase est la racine de l'API ouverte de TikTok (production). Injectable
	// via Config.APIBase pour pointer un httptest en test (aucun appel LIVE).
	DefaultAPIBase = "https://open.tiktokapis.com"

	// VaultConnectorID est l'id de coffre des secrets TikTok. Sans segment d'instance
	// (le core est tenant-agnostic : une instance = un coffre).
	VaultConnectorID = "social/tiktok"
	// KeyAccessToken est la clé de coffre du jeton d'accès OAuth2.
	KeyAccessToken = "access_token"

	// initPath initialise un upload de vidéo vers la BOÎTE DE RÉCEPTION (inbox) du
	// compte : le contenu y atterrit en BROUILLON, à publier manuellement par le
	// titulaire. C'est la voie retenue ici. La voie de publication directe
	// (/v2/post/publish/video/init/) exige une application TikTok auditée et publierait
	// sans validation humaine — hors périmètre de cette vague (cf. CLAUDE:SUMMARY).
	initPath = "/v2/post/publish/inbox/video/init/"
	// statusPath interroge l'avancement d'un upload (par publish_id).
	statusPath = "/v2/post/publish/status/fetch/"

	// sourceFileUpload désigne une source de média par transfert de fichier (par
	// opposition à PULL_FROM_URL) : le connecteur PUT les octets vers l'upload_url.
	sourceFileUpload = "FILE_UPLOAD"

	// maxSingleChunkBytes borne la taille d'un envoi mono-chunk (TikTok accepte une
	// vidéo entière en un seul chunk jusqu'à 64 Mio). Au-delà, l'API exige un découpage
	// multi-chunk (5 à 64 Mio par segment) — non requis pour les vidéos courtes visées
	// par une communauté, et signalé par une erreur explicite plutôt que tronqué.
	maxSingleChunkBytes = 64 * 1024 * 1024

	// statusInbox est le statut terminal d'un dépôt réussi en boîte de réception : le
	// brouillon est arrivé, en attente de publication manuelle. statusFailed est l'échec.
	statusInbox  = "SEND_TO_USER_INBOX"
	statusFailed = "FAILED"

	// DraftPending est la valeur de PostRef.URL : un brouillon n'a PAS d'URL publique
	// (il n'est pas publié). Ce marqueur explicite, lisible en clair dans le journal
	// social_post_log, atteste que la publication réelle reste à valider à la main.
	DraftPending = "brouillon soumis — en attente de validation manuelle dans la boîte TikTok"
)

// ErrNoVideo est retourné quand un post TikTok ne porte aucun média vidéo local.
// TikTok est vidéo-only : un texte seul ou une image ne sont pas publiables.
var ErrNoVideo = errors.New("tiktok: une vidéo est requise (TikTok est vidéo-only)")

// ErrVideoTooLarge est retourné quand la vidéo dépasse la limite mono-chunk : l'upload
// multi-chunk n'est pas implémenté dans cette vague (cf. maxSingleChunkBytes).
var ErrVideoTooLarge = errors.New("tiktok: vidéo trop volumineuse pour un envoi mono-chunk (max 64 Mio)")

// Config porte les paramètres NON secrets du connecteur TikTok. Injectée à la bordure
// (cmd), jamais lue dans l'environnement par ce package.
type Config struct {
	// APIBase surcharge la racine de l'API ouverte (test → httptest). Vide → DefaultAPIBase.
	APIBase string
	// TokenURL et AuthorizeURL surchargent les endpoints OAuth2 (test → httptest).
	// Vides → endpoints TikTok de production.
	TokenURL     string
	AuthorizeURL string
	// RedirectURI est l'URI de redirection OAuth2 (callback). Requis pour le flux
	// d'autorisation, non secret. Optionnel pour la seule publication.
	RedirectURI string
}

// Connector implémente social.Network pour TikTok.
type Connector struct {
	vault        *assets.Vault
	apiBase      string // racine API ouverte résolue
	tokenURL     string // endpoint OAuth2 de jeton résolu
	authorizeURL string // endpoint OAuth2 d'autorisation résolu
	redirect     string
	httpCli      *http.Client // client brut (token + init + upload + statut)
}

// New construit un connecteur TikTok. Fail-loud sur Vault nil : un câblage incomplet
// ne doit jamais démarrer silencieusement. httpCli nil → http.DefaultClient.
func New(vault *assets.Vault, cfg Config, httpCli *http.Client) (*Connector, error) {
	if vault == nil {
		return nil, fmt.Errorf("tiktok.New: Vault requis pour le jeton d'accès")
	}
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	authorizeURL := cfg.AuthorizeURL
	if authorizeURL == "" {
		authorizeURL = defaultAuthorizeURL
	}
	if httpCli == nil {
		httpCli = http.DefaultClient
	}
	return &Connector{
		vault:        vault,
		apiBase:      strings.TrimRight(apiBase, "/"),
		tokenURL:     tokenURL,
		authorizeURL: authorizeURL,
		redirect:     cfg.RedirectURI,
		httpCli:      httpCli,
	}, nil
}

// ID retourne l'identifiant de réseau (le worker route par cette clé).
func (c *Connector) ID() string { return NetworkID }

// Publish dépose la vidéo du post en BROUILLON dans la boîte de réception TikTok et
// retourne sa référence native. Le post DOIT porter un média vidéo local (ErrNoVideo
// sinon). Le flux est : init de l'upload (obtention d'un publish_id + upload_url),
// transfert mono-chunk des octets vers l'upload_url (PUT, Content-Range), puis suivi de
// statut par publish_id.
//
// MODÈLE BROUILLON-EN-BOÎTE : la voie inbox NE PUBLIE PAS. Elle dépose un brouillon que
// le titulaire du compte doit publier manuellement depuis l'application TikTok. Le PostRef
// reflète donc « en attente de validation manuelle » : ExternalID = publish_id (suivi),
// URL = DraftPending (jamais une URL publique, car aucun post public n'existe encore),
// CostCents = 0. Le worker journalise ce PostRef en statut 'sent' au sens « brouillon
// soumis avec succès » — distinct d'une publication publique effective, qui reste manuelle.
func (c *Connector) Publish(ctx context.Context, p social.SocialPost) (social.PostRef, error) {
	videoPath, ok := primaryVideo(p.MediaPaths)
	if !ok {
		return social.PostRef{}, ErrNoVideo
	}

	data, err := os.ReadFile(videoPath) //nolint:gosec // chemin média fourni par la bordure, pas par l'utilisateur final
	if err != nil {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish lecture vidéo %q: %w", videoPath, err)
	}
	if len(data) == 0 {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish: vidéo %q vide", videoPath)
	}
	if len(data) > maxSingleChunkBytes {
		return social.PostRef{}, ErrVideoTooLarge
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return social.PostRef{}, err
	}

	publishID, uploadURL, err := c.initUpload(ctx, token, int64(len(data)))
	if err != nil {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish init: %w", err)
	}
	if err := c.uploadVideo(ctx, uploadURL, data); err != nil {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish upload: %w", err)
	}

	status, err := c.fetchStatus(ctx, token, publishID)
	if err != nil {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish statut: %w", err)
	}
	if status == statusFailed {
		return social.PostRef{}, fmt.Errorf("tiktok.Publish: dépôt du brouillon échoué (statut %s)", status)
	}

	return social.PostRef{
		Network:    NetworkID,
		ExternalID: publishID,
		URL:        DraftPending, // pas d'URL publique : brouillon non publié
		CostCents:  0,
	}, nil
}

// initUpload initialise un upload de vidéo vers la boîte de réception : POST initPath
// avec le source_info FILE_UPLOAD (taille, mono-chunk), en-tête Authorization Bearer.
// Retourne le publish_id (suivi de statut) et l'upload_url (cible du transfert).
func (c *Connector) initUpload(ctx context.Context, token string, videoSize int64) (publishID, uploadURL string, err error) {
	body, err := json.Marshal(initReq{SourceInfo: sourceInfo{
		Source:          sourceFileUpload,
		VideoSize:       videoSize,
		ChunkSize:       videoSize, // mono-chunk : un seul segment couvre toute la vidéo
		TotalChunkCount: 1,
	}})
	if err != nil {
		return "", "", fmt.Errorf("marshal source_info: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+initPath, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("requête init: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	var out initResp
	if err := c.doJSON(req, &out); err != nil {
		return "", "", err
	}
	if out.Error.Code != "" && out.Error.Code != "ok" {
		return "", "", fmt.Errorf("erreur API %s: %s", out.Error.Code, out.Error.Message)
	}
	if out.Data.PublishID == "" || out.Data.UploadURL == "" {
		return "", "", fmt.Errorf("réponse init sans publish_id/upload_url")
	}
	return out.Data.PublishID, out.Data.UploadURL, nil
}

// uploadVideo transfère les octets de la vidéo vers l'upload_url retourné par l'init,
// en un seul PUT (Content-Range couvrant toute la vidéo, Content-Type video/mp4). En
// test, l'upload_url pointe le httptest : aucun octet n'atteint TikTok.
func (c *Connector) uploadVideo(ctx context.Context, uploadURL string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("requête upload: %w", err)
	}
	n := len(data)
	req.Header.Set("Content-Type", "video/mp4")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", n-1, n))
	req.ContentLength = int64(n)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("appel upload: %w", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	return nil
}

// fetchStatus interroge l'avancement de l'upload par publish_id : POST statusPath,
// en-tête Authorization Bearer. Retourne le statut TikTok (ex SEND_TO_USER_INBOX pour
// un brouillon déposé, FAILED en cas d'échec).
func (c *Connector) fetchStatus(ctx context.Context, token, publishID string) (string, error) {
	body, err := json.Marshal(statusReq{PublishID: publishID})
	if err != nil {
		return "", fmt.Errorf("marshal statut: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+statusPath, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("requête statut: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	var out statusResp
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Error.Code != "" && out.Error.Code != "ok" {
		return "", fmt.Errorf("erreur API %s: %s", out.Error.Code, out.Error.Message)
	}
	if out.Data.Status == "" {
		return "", fmt.Errorf("réponse statut sans champ status")
	}
	return out.Data.Status, nil
}

// doJSON exécute une requête et désérialise la réponse JSON dans out. Une réponse hors
// 2xx remonte le corps (diagnostic primaire).
func (c *Connector) doJSON(req *http.Request, out any) error {
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("appel: %w", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("lecture réponse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	if out == nil || buf.Len() == 0 {
		return nil
	}
	if err := json.Unmarshal(buf.Bytes(), out); err != nil {
		return fmt.Errorf("décodage réponse: %w", err)
	}
	return nil
}

// accessToken lit le jeton d'accès OAuth2 depuis le coffre. Le plaintext est zéro-out
// par Vault.Use après copie (aucune référence conservée par ce package).
func (c *Connector) accessToken(ctx context.Context) (string, error) {
	token, err := c.vaultGet(ctx, KeyAccessToken)
	if err != nil {
		return "", fmt.Errorf("tiktok.accessToken: %w", err)
	}
	if token == "" {
		return "", fmt.Errorf("tiktok.accessToken: jeton d'accès vide au coffre")
	}
	return token, nil
}

// vaultGet lit une clé du coffre TikTok (copie locale, plaintext zéro-out par Vault.Use).
func (c *Connector) vaultGet(ctx context.Context, key string) (string, error) {
	var out string
	err := c.vault.Use(ctx, VaultConnectorID, key, func(plaintext string) error {
		out = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("tiktok.vaultGet %q: %w", key, err)
	}
	return out, nil
}

// initReq / sourceInfo composent le corps d'init de l'upload (Content Posting API).
type initReq struct {
	SourceInfo sourceInfo `json:"source_info"`
}
type sourceInfo struct {
	Source          string `json:"source"`
	VideoSize       int64  `json:"video_size"`
	ChunkSize       int64  `json:"chunk_size"`
	TotalChunkCount int    `json:"total_chunk_count"`
}

// initResp est la réponse d'init : data (publish_id + upload_url), error (code/message).
type initResp struct {
	Data struct {
		PublishID string `json:"publish_id"`
		UploadURL string `json:"upload_url"`
	} `json:"data"`
	Error apiError `json:"error"`
}

// statusReq / statusResp composent le suivi de statut par publish_id.
type statusReq struct {
	PublishID string `json:"publish_id"`
}
type statusResp struct {
	Data struct {
		Status     string `json:"status"`
		FailReason string `json:"fail_reason"`
	} `json:"data"`
	Error apiError `json:"error"`
}

// apiError est l'enveloppe d'erreur commune des réponses TikTok (code "ok" = succès).
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// videoExtensions liste les extensions reconnues comme vidéo (TikTok est vidéo-only).
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".webm": true, ".m4v": true,
}

// primaryVideo retourne le premier média vidéo LOCAL (chemin de fichier) et true s'il y
// en a un. Les URLs http(s) ne sont pas des fichiers à téléverser ; les médias non vidéo
// sont ignorés (un post sans vidéo n'est pas publiable sur TikTok).
func primaryVideo(paths []string) (string, bool) {
	for _, p := range paths {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			continue
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		if videoExtensions[strings.ToLower(filepath.Ext(p))] {
			return p, true
		}
	}
	return "", false
}
