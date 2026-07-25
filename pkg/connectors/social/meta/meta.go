// CLAUDE:SUMMARY Connecteur réseau Meta (Graph API) — implémente social.Network pour
// les surfaces Instagram (flux container puis publish) et Facebook (feed / videos).
// CLAUDE:WARN Aucun appel LIVE ici hors runtime câblé : la base Graph est INJECTABLE
// (DefaultGraphBase par défaut) pour que les tests pointent un httptest. Secrets lus
// au coffre sous connectorID "social/meta" (clé SANS segment d'instance), jamais en
// dur ni via os.Getenv dans ce package (lecture d'environnement = bordure cmd).
package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

const (
	// DefaultGraphBase est la racine de l'API Graph Meta (production). Injectable
	// via Config.GraphBase pour pointer un httptest en test (aucun appel LIVE).
	DefaultGraphBase = "https://graph.facebook.com"
	// DefaultVersion est la version d'API Graph ciblée. Reels et media_publish IG
	// sont stables depuis v17 ; v21.0 est la version courante au câblage W1.
	DefaultVersion = "v21.0"

	// VaultConnectorID est l'id de coffre des secrets Meta. Sans segment d'instance
	// (le core est tenant-agnostic : une instance = un coffre).
	VaultConnectorID = "social/meta"
	// KeyPageAccessToken est la clé de coffre du Page Access Token (Bearer Graph).
	KeyPageAccessToken = "page_access_token"
	// KeyAppSecret est la clé de coffre du secret d'application (échange de jeton).
	KeyAppSecret = "app_secret"

	// SurfaceInstagram et SurfaceFacebook sont les deux surfaces servies par Meta.
	// L'id de réseau (ID()) d'un connecteur EST sa surface : le worker enregistre une
	// instance par surface et route par cette clé (Publish ne reçoit pas l'id).
	SurfaceInstagram = "instagram"
	SurfaceFacebook  = "facebook"

	// Bornes Reels verticaux (Instagram) : 9:16, 5 à 90 secondes. Le recadrage 9:16
	// est assuré en amont par social/mediaprep ; ces bornes documentent le contrat.
	ReelsAspectW    = 9
	ReelsAspectH    = 16
	ReelsMinSeconds = 5
	ReelsMaxSeconds = 90
)

// Config porte les paramètres NON secrets d'une surface Meta. Injectée à la bordure
// (cmd), jamais lue dans l'environnement par ce package.
type Config struct {
	// Surface vaut SurfaceInstagram ou SurfaceFacebook.
	Surface string
	// TargetID est l'ig-user-id (Instagram) ou le page-id (Facebook) cible.
	TargetID string
	// AppID identifie l'application Meta, requis pour l'échange de jeton long-lived.
	// Optionnel : si vide, le rafraîchissement actif est désactivé (le Page Access
	// Token du coffre est utilisé tel quel).
	AppID string
	// GraphBase surcharge la racine Graph (test → httptest). Vide → DefaultGraphBase.
	GraphBase string
	// Version surcharge la version d'API. Vide → DefaultVersion.
	Version string
}

// Connector implémente social.Network pour une surface Meta (Instagram ou Facebook).
type Connector struct {
	vault   *assets.Vault
	cfg     Config
	graph   string // racine Graph résolue (cfg.GraphBase ou DefaultGraphBase)
	version string // version résolue
	httpCli *http.Client
}

// New construit un connecteur Meta pour une surface. Fail-loud sur configuration
// manquante (surface inconnue, TargetID vide, Vault nil) : un câblage incomplet ne
// doit jamais démarrer silencieusement. httpCli nil → http.DefaultClient.
func New(vault *assets.Vault, cfg Config, httpCli *http.Client) (*Connector, error) {
	if vault == nil {
		return nil, fmt.Errorf("meta.New: Vault requis pour le Page Access Token")
	}
	if cfg.Surface != SurfaceInstagram && cfg.Surface != SurfaceFacebook {
		return nil, fmt.Errorf("meta.New: surface inconnue %q (attendu %q|%q)", cfg.Surface, SurfaceInstagram, SurfaceFacebook)
	}
	if strings.TrimSpace(cfg.TargetID) == "" {
		return nil, fmt.Errorf("meta.New: TargetID requis (ig-user-id ou page-id) pour la surface %q", cfg.Surface)
	}
	graph := cfg.GraphBase
	if graph == "" {
		graph = DefaultGraphBase
	}
	version := cfg.Version
	if version == "" {
		version = DefaultVersion
	}
	if httpCli == nil {
		httpCli = http.DefaultClient
	}
	return &Connector{
		vault:   vault,
		cfg:     cfg,
		graph:   strings.TrimRight(graph, "/"),
		version: version,
		httpCli: httpCli,
	}, nil
}

// ID retourne la surface comme id de réseau (le worker route par cette clé).
func (c *Connector) ID() string { return c.cfg.Surface }

// Publish diffuse le post sur la surface du connecteur et retourne sa référence
// native. L'aiguillage Instagram/Facebook se fait sur la surface configurée.
func (c *Connector) Publish(ctx context.Context, p social.SocialPost) (social.PostRef, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return social.PostRef{}, err
	}
	switch c.cfg.Surface {
	case SurfaceInstagram:
		return c.publishInstagram(ctx, token, p)
	case SurfaceFacebook:
		return c.publishFacebook(ctx, token, p)
	default:
		return social.PostRef{}, fmt.Errorf("meta.Publish: surface inconnue %q", c.cfg.Surface)
	}
}

// publishInstagram suit le contrat Graph en deux temps : création d'un conteneur
// média (POST /{ig-user-id}/media — image_url ou video_url + caption, media_type
// REELS pour la vidéo verticale) puis publication (POST /{ig-user-id}/media_publish
// — creation_id). La référence native est l'id du média publié.
func (c *Connector) publishInstagram(ctx context.Context, token string, p social.SocialPost) (social.PostRef, error) {
	mediaURL, isVid := primaryMedia(p.MediaPaths)

	container := url.Values{}
	container.Set("caption", p.Content)
	switch {
	case isVid:
		container.Set("media_type", "REELS")
		container.Set("video_url", mediaURL)
	case mediaURL != "":
		container.Set("image_url", mediaURL)
	default:
		return social.PostRef{}, fmt.Errorf("meta.publishInstagram: un média (image ou vidéo) est requis pour un post Instagram")
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := c.postForm(ctx, token, c.cfg.TargetID+"/media", container, &created); err != nil {
		return social.PostRef{}, fmt.Errorf("meta.publishInstagram conteneur: %w", err)
	}

	publish := url.Values{}
	publish.Set("creation_id", created.ID)
	var published struct {
		ID string `json:"id"`
	}
	if err := c.postForm(ctx, token, c.cfg.TargetID+"/media_publish", publish, &published); err != nil {
		return social.PostRef{}, fmt.Errorf("meta.publishInstagram publication: %w", err)
	}

	return social.PostRef{
		Network:    c.cfg.Surface,
		ExternalID: published.ID,
		URL:        "https://www.instagram.com/p/" + published.ID,
	}, nil
}

// publishFacebook publie sur la Page : une vidéo via POST /{page-id}/videos
// (file_url + description), sinon un post de feed via POST /{page-id}/feed
// (message + link éventuel). La référence native est l'id du post ou de la vidéo.
func (c *Connector) publishFacebook(ctx context.Context, token string, p social.SocialPost) (social.PostRef, error) {
	mediaURL, isVid := primaryMedia(p.MediaPaths)

	form := url.Values{}
	var path string
	if isVid {
		path = c.cfg.TargetID + "/videos"
		form.Set("description", p.Content)
		form.Set("file_url", mediaURL)
	} else {
		path = c.cfg.TargetID + "/feed"
		form.Set("message", p.Content)
		if link := firstLink(p.MediaPaths); link != "" {
			form.Set("link", link)
		}
	}

	var posted struct {
		ID string `json:"id"`
	}
	if err := c.postForm(ctx, token, path, form, &posted); err != nil {
		return social.PostRef{}, fmt.Errorf("meta.publishFacebook: %w", err)
	}

	return social.PostRef{
		Network:    c.cfg.Surface,
		ExternalID: posted.ID,
		URL:        "https://www.facebook.com/" + posted.ID,
	}, nil
}

// accessToken lit le Page Access Token depuis le coffre. Le plaintext est zéro-out
// par Vault.Use après copie (aucune référence conservée par ce package).
func (c *Connector) accessToken(ctx context.Context) (string, error) {
	var token string
	err := c.vault.Use(ctx, VaultConnectorID, KeyPageAccessToken, func(plaintext string) error {
		token = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("meta.accessToken: %w", err)
	}
	if token == "" {
		return "", fmt.Errorf("meta.accessToken: Page Access Token vide au coffre")
	}
	return token, nil
}

// postForm exécute un POST form-urlencodé vers {graph}/{version}/{path} avec le
// jeton en en-tête Authorization Bearer, et désérialise la réponse JSON dans out.
// Une réponse hors 2xx remonte le corps Graph (diagnostic).
func (c *Connector) postForm(ctx context.Context, token, path string, form url.Values, out any) error {
	endpoint := c.graph + "/" + c.version + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("appel Graph: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("lecture réponse Graph: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Graph HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("décodage réponse Graph: %w", err)
	}
	return nil
}

// videoExtensions liste les extensions traitées comme vidéo (Reels / FB videos).
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".avi": true,
}

// primaryMedia retourne le premier média et indique s'il s'agit d'une vidéo.
func primaryMedia(paths []string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	first := paths[0]
	ext := strings.ToLower(filepath.Ext(first))
	return first, videoExtensions[ext]
}

// firstLink retourne le premier média qui est une URL http(s) non vidéo, sinon "".
func firstLink(paths []string) string {
	for _, p := range paths {
		if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
			continue
		}
		if videoExtensions[strings.ToLower(filepath.Ext(p))] {
			continue
		}
		return p
	}
	return ""
}
