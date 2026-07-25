// CLAUDE:SUMMARY Connecteur réseau LinkedIn — implémente social.Network. Publie via la
// Posts API (POST {base}/rest/posts) en HTTP direct stdlib : corps JSON avec author (URN
// organisation ou membre), commentary (texte) et, si un média image local est présent,
// un content.media référençant un URN d'image obtenu par le flux d'enregistrement d'asset
// (POST /rest/images?action=initializeUpload → PUT des octets vers l'uploadUrl retourné).
// Le post texte seul est le socle ; l'image est l'option implémentée. Coût nul (LinkedIn
// ne facture pas d'API directe) : CostCents reste 0.
// CLAUDE:WARN Aucun appel LIVE : la racine API est INJECTABLE (Config.APIBase → httptest
// en test). Secrets lus au coffre sous "social/linkedin" (clé SANS segment d'instance),
// jamais en dur ni via os.Getenv dans ce package (lecture d'environnement = bordure cmd).
// NOTE ADMINISTRATIVE (hors code) : publier au nom d'une PAGE D'ORGANISATION
// (author=urn:li:organization:…, scope w_organization_social) exige l'accès au
// LinkedIn Partner Program (Community Management API), démarche d'habilitation auprès de
// LinkedIn — elle n'est PAS résolue ici. Sans cette habilitation, seul l'auteur membre
// (urn:li:person:…, scope w_member_social) est publiable.
package linkedin

import (
	"bytes"
	"context"
	"encoding/json"
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
	NetworkID = "linkedin"

	// DefaultAPIBase est la racine de l'API REST de LinkedIn (production). Injectable
	// via Config.APIBase pour pointer un httptest en test (aucun appel LIVE).
	DefaultAPIBase = "https://api.linkedin.com"

	// DefaultVersion est la valeur PAR DÉFAUT de l'en-tête LinkedIn-Version exigé par la
	// Posts API (versionnage mensuel AAAAMM). X-Restli-Protocol-Version fige le protocole
	// Rest.li. Injectable via Config.Version : la Posts API suit une fenêtre de support
	// glissante (~12 mois), l'alignement sur la version courante réelle dépend de la doc
	// LinkedIn (oracle externe) et se câble à la bordure, jamais en dur ici.
	DefaultVersion = "202401"

	// postsPath est l'endpoint de la Posts API (création d'un post).
	postsPath = "/rest/posts"
	// imagesInitPath initialise l'enregistrement d'un asset image (obtention d'un
	// uploadUrl + d'un URN d'image à référencer dans le post).
	imagesInitPath = "/rest/images?action=initializeUpload"

	// VaultConnectorID est l'id de coffre des secrets LinkedIn. Sans segment d'instance
	// (le core est tenant-agnostic : une instance = un coffre).
	VaultConnectorID = "social/linkedin"
	// KeyAccessToken est la clé de coffre du jeton d'accès OAuth2 (Bearer).
	KeyAccessToken = "access_token"
)

// Config porte les paramètres NON secrets du connecteur LinkedIn. Injectée à la bordure
// (cmd), jamais lue dans l'environnement par ce package.
type Config struct {
	// APIBase surcharge la racine API (test → httptest). Vide → DefaultAPIBase.
	APIBase string
	// Version surcharge l'en-tête LinkedIn-Version (versionnage mensuel AAAAMM de la Posts
	// API). Vide → DefaultVersion. Injectable car la version suit une fenêtre de support
	// glissante : la rotation se pilote à la bordure sans toucher au connecteur.
	Version string
	// AuthorURN est l'auteur des posts : URN d'organisation (urn:li:organization:123,
	// requiert l'habilitation Partner Program) ou de membre (urn:li:person:abc). Requis
	// pour publier ; non secret (identifiant public de l'auteur).
	AuthorURN string
	// TokenURL et AuthorizeURL surchargent les endpoints OAuth2 (test → httptest).
	// Vides → endpoints LinkedIn de production.
	TokenURL     string
	AuthorizeURL string
	// RedirectURI est l'URI de redirection OAuth2 (callback). Requis pour le flux
	// d'autorisation, non secret. Optionnel pour la seule publication.
	RedirectURI string
}

// Connector implémente social.Network pour LinkedIn.
type Connector struct {
	vault        *assets.Vault
	apiBase      string // racine API résolue
	version      string // version LinkedIn-Version résolue
	authorURN    string
	tokenURL     string // endpoint OAuth2 de jeton résolu
	authorizeURL string // endpoint OAuth2 d'autorisation résolu
	redirect     string
	httpCli      *http.Client // client brut (token + post + enregistrement/upload d'asset)
}

// New construit un connecteur LinkedIn. Fail-loud sur Vault nil : un câblage incomplet ne
// doit jamais démarrer silencieusement. httpCli nil → http.DefaultClient.
func New(vault *assets.Vault, cfg Config, httpCli *http.Client) (*Connector, error) {
	if vault == nil {
		return nil, fmt.Errorf("linkedin.New: Vault requis pour le jeton d'accès")
	}
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	version := cfg.Version
	if version == "" {
		version = DefaultVersion
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
		version:      version,
		authorURN:    strings.TrimSpace(cfg.AuthorURN),
		tokenURL:     tokenURL,
		authorizeURL: authorizeURL,
		redirect:     cfg.RedirectURI,
		httpCli:      httpCli,
	}, nil
}

// ID retourne l'identifiant de réseau (le worker route par cette clé).
func (c *Connector) ID() string { return NetworkID }

// Publish diffuse le post sur LinkedIn et retourne sa référence native. Si un média image
// local est présent, il est d'abord enregistré (initializeUpload → URN d'image), ses octets
// transférés vers l'uploadUrl retourné, puis le post le référence en content.media. Sinon
// le post est créé en texte seul. L'auteur (Config.AuthorURN) est requis. Le coût LinkedIn
// est nul (CostCents = 0 : pas de tarif d'API direct).
func (c *Connector) Publish(ctx context.Context, p social.SocialPost) (social.PostRef, error) {
	if c.authorURN == "" {
		return social.PostRef{}, fmt.Errorf("linkedin.Publish: AuthorURN requis (urn:li:organization:… ou urn:li:person:…)")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return social.PostRef{}, err
	}

	var imageURN string
	if path, ok := primaryImage(p.MediaPaths); ok {
		imageURN, err = c.uploadImage(ctx, token, path)
		if err != nil {
			return social.PostRef{}, fmt.Errorf("linkedin.Publish enregistrement image: %w", err)
		}
	}

	postURN, err := c.createPost(ctx, token, p.Content, imageURN)
	if err != nil {
		return social.PostRef{}, fmt.Errorf("linkedin.Publish POST /rest/posts: %w", err)
	}

	url := ""
	if postURN != "" {
		url = "https://www.linkedin.com/feed/update/" + postURN
	}
	return social.PostRef{
		Network:    NetworkID,
		ExternalID: postURN,
		URL:        url,
		CostCents:  0,
	}, nil
}

// createPost crée un post via la Posts API (POST /rest/posts). Le corps porte author,
// commentary (texte), visibility PUBLIC, distribution sur le fil principal et
// lifecycleState PUBLISHED ; si imageURN est non vide, content.media le référence.
// L'identifiant du post est retourné par LinkedIn dans l'en-tête x-restli-id.
func (c *Connector) createPost(ctx context.Context, token, content, imageURN string) (string, error) {
	body := postReq{
		Author:                    c.authorURN,
		Commentary:                content,
		Visibility:                "PUBLIC",
		LifecycleState:            "PUBLISHED",
		IsReprintDisabledByAuthor: false,
		Distribution: distribution{
			FeedDistribution:               "MAIN_FEED",
			TargetEntities:                 []string{},
			ThirdPartyDistributionChannels: []string{},
		},
	}
	if imageURN != "" {
		body.Content = &postContent{Media: &postMedia{ID: imageURN}}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal post: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+postsPath, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("requête post: %w", err)
	}
	c.setCommonHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("appel post: %w", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	// L'id du post est l'en-tête x-restli-id (URN urn:li:share:… ou urn:li:ugcPost:…).
	postURN := resp.Header.Get("x-restli-id")
	if postURN == "" {
		postURN = resp.Header.Get("x-linkedin-id")
	}
	return postURN, nil
}

// uploadImage exécute le flux d'enregistrement d'asset image : POST initializeUpload
// (réserve un URN d'image + un uploadUrl pour l'auteur), puis PUT des octets de l'image
// vers cet uploadUrl. Retourne l'URN d'image à référencer dans le post.
func (c *Connector) uploadImage(ctx context.Context, token, path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // chemin média fourni par la bordure, pas par l'utilisateur final
	if err != nil {
		return "", fmt.Errorf("lecture image %q: %w", path, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("image %q vide", path)
	}

	// initializeUpload : réserve l'URN d'image et l'uploadUrl pour l'auteur.
	initBody, err := json.Marshal(imageInitReq{InitializeUploadRequest: initializeUploadRequest{Owner: c.authorURN}})
	if err != nil {
		return "", fmt.Errorf("marshal initializeUpload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+imagesInitPath, bytes.NewReader(initBody))
	if err != nil {
		return "", fmt.Errorf("requête initializeUpload: %w", err)
	}
	c.setCommonHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	var initResp imageInitResp
	if err := c.doJSON(req, &initResp); err != nil {
		return "", fmt.Errorf("initializeUpload: %w", err)
	}
	if initResp.Value.Image == "" || initResp.Value.UploadURL == "" {
		return "", fmt.Errorf("réponse initializeUpload sans image/uploadUrl")
	}

	// Transfert des octets vers l'uploadUrl (PUT, jeton Bearer).
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPut, initResp.Value.UploadURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("requête upload image: %w", err)
	}
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.ContentLength = int64(len(data))
	upResp, err := c.httpCli.Do(upReq)
	if err != nil {
		return "", fmt.Errorf("appel upload image: %w", err)
	}
	defer upResp.Body.Close()
	ubuf := new(bytes.Buffer)
	_, _ = ubuf.ReadFrom(upResp.Body)
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		return "", fmt.Errorf("upload image HTTP %d: %s", upResp.StatusCode, strings.TrimSpace(ubuf.String()))
	}
	return initResp.Value.Image, nil
}

// setCommonHeaders pose les en-têtes communs des appels REST LinkedIn : Authorization
// Bearer, LinkedIn-Version (versionnage mensuel exigé par la Posts API) et
// X-Restli-Protocol-Version (protocole Rest.li 2.0.0).
func (c *Connector) setCommonHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("LinkedIn-Version", c.version)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
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

// accessToken lit le jeton d'accès OAuth2 depuis le coffre. Le plaintext est zéro-out par
// Vault.Use après copie (aucune référence conservée par ce package).
func (c *Connector) accessToken(ctx context.Context) (string, error) {
	token, err := c.vaultGet(ctx, KeyAccessToken)
	if err != nil {
		return "", fmt.Errorf("linkedin.accessToken: %w", err)
	}
	if token == "" {
		return "", fmt.Errorf("linkedin.accessToken: jeton d'accès vide au coffre")
	}
	return token, nil
}

// vaultGet lit une clé du coffre LinkedIn (copie locale, plaintext zéro-out par Vault.Use).
func (c *Connector) vaultGet(ctx context.Context, key string) (string, error) {
	var out string
	err := c.vault.Use(ctx, VaultConnectorID, key, func(plaintext string) error {
		out = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("linkedin.vaultGet %q: %w", key, err)
	}
	return out, nil
}

// postReq compose le corps de création d'un post (Posts API).
type postReq struct {
	Author                    string       `json:"author"`
	Commentary                string       `json:"commentary"`
	Visibility                string       `json:"visibility"`
	LifecycleState            string       `json:"lifecycleState"`
	IsReprintDisabledByAuthor bool         `json:"isReprintDisabledByAuthor"`
	Distribution              distribution `json:"distribution"`
	Content                   *postContent `json:"content,omitempty"`
}
type distribution struct {
	FeedDistribution               string   `json:"feedDistribution"`
	TargetEntities                 []string `json:"targetEntities"`
	ThirdPartyDistributionChannels []string `json:"thirdPartyDistributionChannels"`
}
type postContent struct {
	Media *postMedia `json:"media,omitempty"`
}
type postMedia struct {
	ID string `json:"id"`
}

// imageInitReq / imageInitResp composent le flux d'enregistrement d'asset image.
type imageInitReq struct {
	InitializeUploadRequest initializeUploadRequest `json:"initializeUploadRequest"`
}
type initializeUploadRequest struct {
	Owner string `json:"owner"`
}
type imageInitResp struct {
	Value struct {
		UploadURL string `json:"uploadUrl"`
		Image     string `json:"image"`
	} `json:"value"`
}

// imageExtensions liste les extensions reconnues comme image (asset téléversable).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// primaryImage retourne le premier média image LOCAL (chemin de fichier) et true s'il y en
// a un. Les URLs http(s) ne sont pas des fichiers à téléverser ; les médias non image (ex.
// vidéo) sont ignorés (la vidéo LinkedIn n'est pas couverte dans cette vague).
func primaryImage(paths []string) (string, bool) {
	for _, p := range paths {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			continue
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		if imageExtensions[strings.ToLower(filepath.Ext(p))] {
			return p, true
		}
	}
	return "", false
}
