// CLAUDE:SUMMARY Flux OAuth2 Authorization Code + PKCE pour TikTok — URL d'autorisation
// (client_key, scope video.upload, code_challenge S256), échange du code contre
// access/refresh token, rafraîchissement. TikTok identifie l'application par client_key
// (et non client_id) et attend les identifiants dans le CORPS form-urlencodé (pas de
// Basic auth). Jetons rotationnés au coffre sous "social/tiktok".
// CLAUDE:WARN Aucun secret en dur ni env : client_key / client_secret / refresh_token
// lus via Vault.Use (zéro-out après usage). L'endpoint de jeton est INJECTABLE
// (Config.TokenURL → httptest), aucun appel LIVE n'atteint TikTok.
package tiktok

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
	// KeyClientKey et KeyClientSecret sont les clés de coffre de l'application OAuth2
	// TikTok. TikTok nomme l'identifiant public client_key (pas client_id).
	KeyClientKey    = "client_key"
	KeyClientSecret = "client_secret"
	// KeyRefreshToken est la clé de coffre du jeton de rafraîchissement OAuth2.
	KeyRefreshToken = "refresh_token"

	// defaultTokenURL et defaultAuthorizeURL sont les endpoints OAuth2 de TikTok
	// (production). Surchargés en test via Config.TokenURL / Config.AuthorizeURL.
	defaultTokenURL     = "https://open.tiktokapis.com/v2/oauth/token/"
	defaultAuthorizeURL = "https://www.tiktok.com/v2/auth/authorize/"
)

// DefaultScopes sont les portées demandées. video.upload autorise le dépôt d'une vidéo
// en boîte de réception (brouillon) ; video.publish (publication directe) exige une
// application auditée et n'est pas demandé ici (cf. modèle brouillon-en-boîte).
var DefaultScopes = []string{"video.upload"}

// ErrNoRefreshToken est retourné quand un rafraîchissement est sollicité sans
// refresh_token au coffre (le flux d'autorisation initial n'a pas encore eu lieu).
var ErrNoRefreshToken = errors.New("tiktok: aucun refresh_token au coffre")

// GenerateVerifier produit un code_verifier PKCE (43 à 128 caractères base64url sans
// remplissage), source crypto/rand. À conserver côté serveur entre l'autorisation et
// l'échange du code.
func GenerateVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("tiktok.GenerateVerifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// challengeS256 dérive le code_challenge (méthode S256) d'un verifier.
func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizeURL construit l'URL d'autorisation OAuth2 + PKCE (response_type=code, scopes,
// code_challenge S256, state). Le client_key est lu au coffre. Aucune requête réseau
// n'est émise ici : l'usager est redirigé vers cette URL.
func (c *Connector) AuthorizeURL(ctx context.Context, state, verifier string) (string, error) {
	if strings.TrimSpace(c.redirect) == "" {
		return "", fmt.Errorf("tiktok.AuthorizeURL: RedirectURI requis")
	}
	clientKey, err := c.vaultGet(ctx, KeyClientKey)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client_key", clientKey)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(DefaultScopes, ","))
	q.Set("redirect_uri", c.redirect)
	q.Set("state", state)
	q.Set("code_challenge", challengeS256(verifier))
	q.Set("code_challenge_method", "S256")
	return c.authorizeURL + "?" + q.Encode(), nil
}

// ExchangeCode échange le code d'autorisation (grant_type=authorization_code) contre un
// access_token et un refresh_token, en présentant le code_verifier PKCE, puis rotationne
// les deux jetons au coffre. Retourne l'access_token.
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

// tokenRequest exécute un POST vers l'endpoint de jeton OAuth2. TikTok attend
// client_key et client_secret dans le CORPS form-urlencodé (pas de Basic auth),
// désérialise la réponse et rotationne access_token (+ refresh_token si fourni) au coffre.
func (c *Connector) tokenRequest(ctx context.Context, form url.Values) (string, error) {
	clientKey, err := c.vaultGet(ctx, KeyClientKey)
	if err != nil {
		return "", err
	}
	clientSecret, err := c.vaultGet(ctx, KeyClientSecret)
	if err != nil {
		return "", err
	}
	form.Set("client_key", clientKey)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("tiktok.tokenRequest requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("tiktok.tokenRequest appel: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("tiktok.tokenRequest lecture réponse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tiktok.tokenRequest HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		OpenID       string `json:"open_id"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("tiktok.tokenRequest décodage: %w", err)
	}
	if tok.Error != "" {
		return "", fmt.Errorf("tiktok.tokenRequest erreur %s: %s", tok.Error, tok.ErrorDesc)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("tiktok.tokenRequest: réponse sans access_token")
	}

	// Set (upsert) : à l'échange initial, les jetons n'existent pas encore au coffre.
	if err := c.vault.Set(ctx, VaultConnectorID, KeyAccessToken, tok.AccessToken, ""); err != nil {
		return "", fmt.Errorf("tiktok.tokenRequest stockage access_token: %w", err)
	}
	if tok.RefreshToken != "" {
		if err := c.vault.Set(ctx, VaultConnectorID, KeyRefreshToken, tok.RefreshToken, ""); err != nil {
			return "", fmt.Errorf("tiktok.tokenRequest stockage refresh_token: %w", err)
		}
	}
	return tok.AccessToken, nil
}
