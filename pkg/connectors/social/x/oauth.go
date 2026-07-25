// CLAUDE:SUMMARY Flux OAuth2 Authorization Code + PKCE pour X — URL d'autorisation
// (code_challenge S256), échange du code contre access/refresh token, rafraîchissement.
// Jetons rotationnés au coffre sous "social/x".
// CLAUDE:WARN Aucun secret en dur ni env : client_id / client_secret / refresh_token
// lus via Vault.Use (zéro-out après usage). L'endpoint de jeton est la base API
// INJECTÉE (test → httptest), aucun appel LIVE n'atteint X.
package x

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	// KeyClientID et KeyClientSecret sont les clés de coffre de l'application OAuth2
	// (client confidentiel X : Basic auth sur l'endpoint de jeton).
	KeyClientID     = "client_id"
	KeyClientSecret = "client_secret"
	// KeyRefreshToken est la clé de coffre du jeton de rafraîchissement OAuth2.
	KeyRefreshToken = "refresh_token"

	// authorizePath et tokenPath sont les chemins OAuth2 de X.
	authorizePath = "/i/oauth2/authorize"
	tokenPath     = "/2/oauth2/token"
	// DefaultAuthorizeBase est la racine d'autorisation (interactive, navigateur).
	// Aucune requête serveur ne l'atteint : l'URL est seulement construite.
	DefaultAuthorizeBase = "https://x.com"
)

// DefaultScopes sont les portées demandées : lecture/écriture de tweets, lecture du
// profil, et offline.access (indispensable pour obtenir un refresh_token).
var DefaultScopes = []string{"tweet.read", "tweet.write", "users.read", "offline.access"}

// ErrNoRefreshToken est retourné quand un rafraîchissement est sollicité sans
// refresh_token au coffre (le flux d'autorisation initial n'a pas encore eu lieu).
var ErrNoRefreshToken = errors.New("x: aucun refresh_token au coffre")

// GenerateVerifier produit un code_verifier PKCE (43 à 128 caractères base64url sans
// remplissage), source crypto/rand. À conserver côté serveur entre l'autorisation et
// l'échange du code.
func GenerateVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("x.GenerateVerifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// challengeS256 dérive le code_challenge (méthode S256) d'un verifier.
func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizeURL construit l'URL d'autorisation OAuth2 + PKCE (response_type=code,
// scopes, code_challenge S256, state). Le client_id est lu au coffre. Aucune requête
// réseau n'est émise ici : l'usager est redirigé vers cette URL.
func (c *Connector) AuthorizeURL(ctx context.Context, state, verifier string) (string, error) {
	if strings.TrimSpace(c.redirect) == "" {
		return "", fmt.Errorf("x.AuthorizeURL: RedirectURI requis")
	}
	clientID, err := c.vaultGet(ctx, KeyClientID)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", c.redirect)
	q.Set("scope", strings.Join(DefaultScopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challengeS256(verifier))
	q.Set("code_challenge_method", "S256")
	return DefaultAuthorizeBase + authorizePath + "?" + q.Encode(), nil
}

// ExchangeCode échange le code d'autorisation (grant_type=authorization_code) contre
// un access_token et un refresh_token, en présentant le code_verifier PKCE, puis
// rotationne les deux jetons au coffre. Retourne l'access_token.
func (c *Connector) ExchangeCode(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirect)
	form.Set("code_verifier", verifier)
	return c.tokenRequest(ctx, form)
}

// RefreshToken échange le refresh_token courant (grant_type=refresh_token) contre un
// nouveau couple access/refresh token, puis rotationne au coffre. Retourne le nouvel
// access_token.
func (c *Connector) RefreshToken(ctx context.Context) (string, error) {
	refresh, err := c.vaultGet(ctx, KeyRefreshToken)
	if err != nil {
		return "", err
	}
	if refresh == "" {
		return "", ErrNoRefreshToken
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	return c.tokenRequest(ctx, form)
}

// tokenRequest exécute un POST vers l'endpoint de jeton OAuth2 avec authentification
// client confidentiel (Basic client_id:client_secret), désérialise la réponse et
// rotationne access_token (+ refresh_token si fourni) au coffre.
func (c *Connector) tokenRequest(ctx context.Context, form url.Values) (string, error) {
	clientID, err := c.vaultGet(ctx, KeyClientID)
	if err != nil {
		return "", err
	}
	clientSecret, err := c.vaultGet(ctx, KeyClientSecret)
	if err != nil {
		return "", err
	}
	// X exige client_id dans le corps pour les clients publics ; pour un client
	// confidentiel, l'authentification se fait en Basic. On fournit les deux.
	form.Set("client_id", clientID)

	endpoint := c.apiBase + tokenPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("x.tokenRequest requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// SetBasicAuth attend les valeurs BRUTES (il assure lui-même l'encodage base64) :
	// un QueryEscape préalable sur-encoderait un secret contenant +, / ou =.
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("x.tokenRequest appel: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("x.tokenRequest lecture réponse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("x.tokenRequest HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("x.tokenRequest décodage: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("x.tokenRequest: réponse sans access_token")
	}

	// Set (upsert) plutôt que Rotate : à l'échange initial, access_token / refresh_token
	// n'existent pas encore au coffre. Set crée ou remplace de façon idempotente.
	if err := c.vault.Set(ctx, VaultConnectorID, KeyAccessToken, tok.AccessToken, ""); err != nil {
		return "", fmt.Errorf("x.tokenRequest stockage access_token: %w", err)
	}
	if tok.RefreshToken != "" {
		if err := c.vault.Set(ctx, VaultConnectorID, KeyRefreshToken, tok.RefreshToken, ""); err != nil {
			return "", fmt.Errorf("x.tokenRequest stockage refresh_token: %w", err)
		}
	}
	return tok.AccessToken, nil
}

// vaultGet lit une clé du coffre X (copie locale, plaintext zéro-out par Vault.Use).
func (c *Connector) vaultGet(ctx context.Context, key string) (string, error) {
	var out string
	err := c.vault.Use(ctx, VaultConnectorID, key, func(plaintext string) error {
		out = plaintext
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("x.vaultGet %q: %w", key, err)
	}
	return out, nil
}
