// CLAUDE:SUMMARY Flux OAuth2 (golang.org/x/oauth2/google) pour YouTube — URL
// d'autorisation (access_type=offline pour obtenir un refresh_token), échange du code,
// rafraîchissement via TokenSource. Jetons rotationnés au coffre sous "social/youtube".
// CLAUDE:WARN Aucun secret en dur ni env : client_id / client_secret / refresh_token
// lus via Vault.Use (zéro-out après usage). Les endpoints OAuth2 sont INJECTABLES
// (Config.TokenURL/AuthURL → httptest) et le client HTTP est passé par le contexte
// (oauth2.HTTPClient), garantissant qu'aucun appel n'atteint googleapis.com en test.
package youtube

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/oauth2"
	ytapi "google.golang.org/api/youtube/v3"
)

const (
	// KeyClientID et KeyClientSecret sont les clés de coffre de l'application OAuth2.
	KeyClientID     = "client_id"
	KeyClientSecret = "client_secret"
	// KeyAccessToken et KeyRefreshToken sont les clés de coffre des jetons OAuth2.
	KeyAccessToken  = "access_token"
	KeyRefreshToken = "refresh_token"

	// googleAuthURL et googleTokenURL sont les endpoints OAuth2 de Google (production).
	// Surchargeables via Config.AuthURL/TokenURL (test → httptest).
	googleAuthURL  = "https://accounts.google.com/o/oauth2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

// DefaultScopes sont les portées demandées : téléversement de vidéos (youtube.upload)
// et gestion du compte (youtube). offline est obtenu via access_type=offline.
var DefaultScopes = []string{ytapi.YoutubeUploadScope, ytapi.YoutubeScope}

// ErrNoRefreshToken est retourné quand un rafraîchissement est sollicité sans
// refresh_token au coffre (le flux d'autorisation initial n'a pas encore eu lieu).
var ErrNoRefreshToken = errors.New("youtube: aucun refresh_token au coffre")

// oauthConfig construit la configuration OAuth2 à partir des secrets d'application lus
// au coffre et des endpoints résolus (production ou injectés).
func (c *Connector) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	clientID, err := c.vaultGet(ctx, KeyClientID)
	if err != nil {
		return nil, err
	}
	clientSecret, err := c.vaultGet(ctx, KeyClientSecret)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  c.redirect,
		Scopes:       DefaultScopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  c.authURL,
			TokenURL: c.tokenURL,
		},
	}, nil
}

// oauthCtx injecte le client HTTP de base dans le contexte OAuth2 (oauth2.HTTPClient),
// de sorte que les appels au endpoint de jeton empruntent le httptest en test. En
// production (httpCli nil) le contexte est inchangé : oauth2 utilise son client par défaut.
func (c *Connector) oauthCtx(ctx context.Context) context.Context {
	if c.httpCli == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, c.httpCli)
}

// AuthorizeURL construit l'URL d'autorisation OAuth2 (access_type=offline pour obtenir
// un refresh_token, prompt=consent pour forcer son émission). Aucune requête réseau
// n'est émise : l'usager est redirigé vers cette URL.
func (c *Connector) AuthorizeURL(ctx context.Context, state string) (string, error) {
	if c.redirect == "" {
		return "", fmt.Errorf("youtube.AuthorizeURL: RedirectURI requis")
	}
	cfg, err := c.oauthConfig(ctx)
	if err != nil {
		return "", err
	}
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), nil
}

// ExchangeCode échange le code d'autorisation contre un access_token et un
// refresh_token, puis rotationne les deux jetons au coffre. Retourne l'access_token.
func (c *Connector) ExchangeCode(ctx context.Context, code string) (string, error) {
	cfg, err := c.oauthConfig(ctx)
	if err != nil {
		return "", err
	}
	tok, err := cfg.Exchange(c.oauthCtx(ctx), code)
	if err != nil {
		return "", fmt.Errorf("youtube.ExchangeCode: %w", err)
	}
	if err := c.persistToken(ctx, tok.AccessToken, tok.RefreshToken); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// RefreshToken échange le refresh_token courant contre un nouvel access_token via le
// TokenSource oauth2, puis rotationne au coffre. Retourne le nouvel access_token.
func (c *Connector) RefreshToken(ctx context.Context) (string, error) {
	refresh, err := c.vaultGetOptional(ctx, KeyRefreshToken)
	if err != nil {
		return "", err
	}
	if refresh == "" {
		return "", ErrNoRefreshToken
	}
	cfg, err := c.oauthConfig(ctx)
	if err != nil {
		return "", err
	}
	tok, err := cfg.TokenSource(c.oauthCtx(ctx), &oauth2.Token{RefreshToken: refresh}).Token()
	if err != nil {
		return "", fmt.Errorf("youtube.RefreshToken: %w", err)
	}
	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = refresh // Google ne réémet pas toujours le refresh_token.
	}
	if err := c.persistToken(ctx, tok.AccessToken, newRefresh); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// tokenSource construit la source de jetons utilisée par Publish. Le jeton d'accès est
// lu au coffre ; s'il existe aussi un refresh_token et les secrets d'application, la
// source rafraîchit automatiquement (et persiste tout jeton renouvelé au coffre). Sans
// refresh_token, une source statique est retournée (le jeton d'accès est utilisé tel quel).
func (c *Connector) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	access, err := c.vaultGet(ctx, KeyAccessToken)
	if err != nil {
		return nil, err
	}
	if access == "" {
		return nil, fmt.Errorf("youtube.tokenSource: jeton d'accès vide au coffre")
	}
	refresh, err := c.vaultGetOptional(ctx, KeyRefreshToken)
	if err != nil {
		return nil, err
	}
	tok := &oauth2.Token{AccessToken: access, RefreshToken: refresh}
	if refresh == "" {
		return oauth2.StaticTokenSource(tok), nil
	}
	cfg, err := c.oauthConfig(ctx)
	if err != nil {
		return nil, err
	}
	inner := oauth2.ReuseTokenSource(tok, cfg.TokenSource(c.oauthCtx(ctx), tok))
	return &persistingSource{conn: c, ctx: ctx, inner: inner, lastAccess: access}, nil
}

// persistingSource enveloppe une TokenSource pour rotationner au coffre tout jeton
// renouvelé (le refresh oauth2 vit en mémoire ; le coffre est la mémoire durable).
type persistingSource struct {
	conn       *Connector
	ctx        context.Context
	inner      oauth2.TokenSource
	mu         sync.Mutex
	lastAccess string
}

// Token retourne le jeton courant et, s'il a changé (refresh), le persiste au coffre.
func (s *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := s.inner.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.AccessToken != s.lastAccess {
		_ = s.conn.persistToken(s.ctx, tok.AccessToken, tok.RefreshToken)
		s.lastAccess = tok.AccessToken
	}
	return tok, nil
}

// persistToken rotationne access_token (+ refresh_token si non vide) au coffre. Set
// (upsert) est utilisé : à l'échange initial les clés n'existent pas encore.
func (c *Connector) persistToken(ctx context.Context, access, refresh string) error {
	if access != "" {
		if err := c.vault.Set(ctx, VaultConnectorID, KeyAccessToken, access, ""); err != nil {
			return fmt.Errorf("youtube.persistToken access_token: %w", err)
		}
	}
	if refresh != "" {
		if err := c.vault.Set(ctx, VaultConnectorID, KeyRefreshToken, refresh, ""); err != nil {
			return fmt.Errorf("youtube.persistToken refresh_token: %w", err)
		}
	}
	return nil
}

// vaultGet lit une clé du coffre YouTube (copie locale, plaintext zéro-out par Vault.Use).
func (c *Connector) vaultGet(ctx context.Context, key string) (string, error) {
	var out string
	err := c.vault.Use(ctx, VaultConnectorID, key, func(plaintext string) error {
		out = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("youtube.vaultGet %q: %w", key, err)
	}
	return out, nil
}

// vaultGetOptional lit une clé du coffre, retournant "" sans erreur si elle est absente
// (utilisé pour les secrets facultatifs : refresh_token avant le premier flux OAuth).
func (c *Connector) vaultGetOptional(ctx context.Context, key string) (string, error) {
	keys, err := c.vault.List(ctx, VaultConnectorID)
	if err != nil {
		return "", fmt.Errorf("youtube.vaultGetOptional liste %q: %w", key, err)
	}
	for _, k := range keys {
		if k == key {
			return c.vaultGet(ctx, key)
		}
	}
	return "", nil
}
