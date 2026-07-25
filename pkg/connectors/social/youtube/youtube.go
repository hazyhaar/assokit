// CLAUDE:SUMMARY Connecteur réseau YouTube — implémente social.Network. YouTube est
// VIDÉO-ONLY : Publish exige un média vidéo dans le SocialPost et téléverse via la
// Data API v3 videos.insert (upload resumable, part=snippet,status). L'endpoint API est
// INJECTABLE (option.WithEndpoint) et le client HTTP authentifié est construit par le
// connecteur (oauth2.Transport) pour pointer un httptest en test (aucun appel LIVE).
// CLAUDE:WARN Secrets lus au coffre sous "social/youtube" (clé SANS segment d'instance),
// jamais en dur ni via os.Getenv dans ce package (lecture d'environnement = bordure cmd).
// Coût YouTube = quota d'API, pas de tarif monétaire direct : CostCents reste 0.
// Le projet d'upload n'étant pas vérifié par YouTube, les vidéos restent en privacyStatus
// "private" par défaut (la vérification de conformité est hors périmètre de cette vague).
package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	ytapi "google.golang.org/api/youtube/v3"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

const (
	// NetworkID est l'identifiant stable du réseau (le worker route par cette clé).
	NetworkID = "youtube"

	// DefaultEndpoint est la racine de la Data API v3 de YouTube (production).
	// Injectable via Config.Endpoint (option.WithEndpoint) pour pointer un httptest.
	DefaultEndpoint = "https://youtube.googleapis.com/"

	// VaultConnectorID est l'id de coffre des secrets YouTube. Sans segment d'instance
	// (le core est tenant-agnostic : une instance = un coffre).
	VaultConnectorID = "social/youtube"

	// DefaultPrivacyStatus est le statut de confidentialité par défaut des uploads. Le
	// projet d'upload n'étant pas (encore) vérifié par YouTube, "private" est imposé :
	// une chaîne non vérifiée ne peut publier en public au-delà d'une limite, et un
	// upload public non maîtrisé serait un geste de modération non assumé.
	DefaultPrivacyStatus = "private"

	// uploadChunkSize force l'upload resumable (chunk de 256 Kio, multiple minimal exigé
	// par l'API). Toute vidéo réelle dépasse un chunk : la voie resumable est donc
	// effectivement empruntée (initiation puis transfert chunké).
	uploadChunkSize = 256 * 1024

	// shortsTag est le marqueur ajouté à la description pour signaler un Short à YouTube.
	shortsTag = "#Shorts"

	// maxTitleRunes borne la longueur du titre (contrainte API YouTube : 100 caractères).
	maxTitleRunes = 100
)

// ErrNoVideo est retourné quand un post YouTube ne porte aucun média vidéo local.
// YouTube est vidéo-only : un texte seul ou une image ne sont pas publiables.
var ErrNoVideo = errors.New("youtube: une vidéo est requise (YouTube est vidéo-only)")

// Config porte les paramètres NON secrets du connecteur YouTube. Injectée à la bordure
// (cmd), jamais lue dans l'environnement par ce package.
type Config struct {
	// Endpoint surcharge la racine de la Data API v3 (test → httptest). Vide → DefaultEndpoint.
	Endpoint string
	// TokenURL et AuthURL surchargent les endpoints OAuth2 (test → httptest). Vides → Google.
	TokenURL string
	AuthURL  string
	// RedirectURI est l'URI de redirection OAuth2 (callback). Requis pour le flux
	// d'autorisation, non secret. Optionnel pour la seule publication.
	RedirectURI string
	// PrivacyStatus surcharge le statut de confidentialité des uploads. Vide → DefaultPrivacyStatus.
	PrivacyStatus string
}

// Connector implémente social.Network pour YouTube.
type Connector struct {
	vault    *assets.Vault
	endpoint string // racine API v3 résolue
	tokenURL string // endpoint OAuth2 de jeton résolu
	authURL  string // endpoint OAuth2 d'autorisation résolu
	redirect string
	privacy  string
	httpCli  *http.Client // client de transport de BASE (test injection) ; nil → défaut
}

// New construit un connecteur YouTube. Fail-loud sur Vault nil : un câblage incomplet
// ne doit jamais démarrer silencieusement. httpCli nil → transport par défaut.
func New(vault *assets.Vault, cfg Config, httpCli *http.Client) (*Connector, error) {
	if vault == nil {
		return nil, fmt.Errorf("youtube.New: Vault requis pour le jeton d'accès")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	authURL := cfg.AuthURL
	tokenURL := cfg.TokenURL
	if authURL == "" {
		authURL = googleAuthURL
	}
	if tokenURL == "" {
		tokenURL = googleTokenURL
	}
	privacy := cfg.PrivacyStatus
	if privacy == "" {
		privacy = DefaultPrivacyStatus
	}
	return &Connector{
		vault:    vault,
		endpoint: endpoint,
		tokenURL: tokenURL,
		authURL:  authURL,
		redirect: cfg.RedirectURI,
		privacy:  privacy,
		httpCli:  httpCli,
	}, nil
}

// ID retourne l'identifiant de réseau (le worker route par cette clé).
func (c *Connector) ID() string { return NetworkID }

// Publish téléverse la vidéo du post vers YouTube via videos.insert (part=snippet,status,
// upload resumable) et retourne sa référence native. Le post DOIT porter un média vidéo
// local (ErrNoVideo sinon). Le titre est dérivé de la première ligne du contenu, la
// description du contenu complet ; si le post est marqué Short, #Shorts est ajouté à la
// description. Le coût YouTube est un quota d'API, pas un tarif monétaire : CostCents = 0.
func (c *Connector) Publish(ctx context.Context, p social.SocialPost) (social.PostRef, error) {
	videoPath, ok := primaryVideo(p.MediaPaths)
	if !ok {
		return social.PostRef{}, ErrNoVideo
	}

	ts, err := c.tokenSource(ctx)
	if err != nil {
		return social.PostRef{}, err
	}
	authClient := &http.Client{Transport: &oauth2.Transport{Source: ts, Base: c.baseTransport()}}
	svc, err := ytapi.NewService(ctx, option.WithHTTPClient(authClient), option.WithEndpoint(c.endpoint))
	if err != nil {
		return social.PostRef{}, fmt.Errorf("youtube.Publish service: %w", err)
	}

	f, err := os.Open(videoPath) //nolint:gosec // chemin média fourni par la bordure, pas par l'utilisateur final
	if err != nil {
		return social.PostRef{}, fmt.Errorf("youtube.Publish ouverture vidéo %q: %w", videoPath, err)
	}
	defer f.Close()

	desc := p.Content
	if isShort(p) {
		desc = ensureShortsTag(desc)
	}
	video := &ytapi.Video{
		Snippet: &ytapi.VideoSnippet{
			Title:       title(p.Content),
			Description: desc,
		},
		Status: &ytapi.VideoStatus{
			PrivacyStatus: c.privacy,
		},
	}

	out, err := svc.Videos.
		Insert([]string{"snippet", "status"}, video).
		Media(f, googleapi.ChunkSize(uploadChunkSize)).
		Context(ctx).
		Do()
	if err != nil {
		return social.PostRef{}, fmt.Errorf("youtube.Publish videos.insert: %w", err)
	}
	if out.Id == "" {
		return social.PostRef{}, fmt.Errorf("youtube.Publish: réponse videos.insert sans id")
	}

	return social.PostRef{
		Network:    NetworkID,
		ExternalID: out.Id,
		URL:        "https://www.youtube.com/watch?v=" + out.Id,
		CostCents:  0,
	}, nil
}

// baseTransport retourne le transport HTTP de base à habiller d'authentification. En
// test il pointe le httptest injecté (c.httpCli.Transport) ; en production il vaut le
// transport par défaut.
func (c *Connector) baseTransport() http.RoundTripper {
	if c.httpCli != nil && c.httpCli.Transport != nil {
		return c.httpCli.Transport
	}
	return http.DefaultTransport
}

// videoExtensions liste les extensions reconnues comme vidéo (YouTube est vidéo-only).
var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true,
	".avi": true, ".mkv": true, ".flv": true, ".wmv": true,
}

// primaryVideo retourne le premier média vidéo LOCAL (chemin de fichier) et true s'il y
// en a un. Les URLs http(s) ne sont pas des fichiers à téléverser ; les médias non vidéo
// sont ignorés (un post sans vidéo n'est pas publiable sur YouTube).
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

// title dérive le titre de la première ligne non vide du contenu, tronqué à
// maxTitleRunes. Un contenu vide retombe sur un titre générique (le titre est requis
// par l'API).
func title(content string) string {
	line := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		line = content[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "Vidéo"
	}
	r := []rune(line)
	if len(r) > maxTitleRunes {
		return string(r[:maxTitleRunes])
	}
	return line
}

// isShort indique si le post doit être marqué Short. YouTube classe un Short par la
// durée (<60 s) et le ratio vertical (9:16) de la vidéo elle-même ; sonder ces
// caractéristiques exigerait un démuxage média (ffprobe / lecture des atomes MP4),
// hors périmètre d'un binaire CGO_ENABLED=0. La convention retenue est donc explicite :
// le contenu porte un marqueur "#short"/"#shorts" (cf. SocialPost.Content), ce qui
// déclenche l'ajout de #Shorts à la description envoyée à YouTube.
func isShort(p social.SocialPost) bool {
	lower := strings.ToLower(p.Content)
	return strings.Contains(lower, "#shorts") || strings.Contains(lower, "#short")
}

// ensureShortsTag garantit la présence du tag #Shorts dans la description (sans
// doublonner si une variante est déjà présente).
func ensureShortsTag(desc string) string {
	if strings.Contains(strings.ToLower(desc), "#shorts") {
		return desc
	}
	if strings.TrimSpace(desc) == "" {
		return shortsTag
	}
	return desc + "\n\n" + shortsTag
}
