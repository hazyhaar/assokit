// CLAUDE:SUMMARY Flux OAuth2 Authorization Code (3-legged) pour LinkedIn — URL
// d'autorisation (response_type=code, scopes w_member_social / w_organization_social),
// échange du code contre access/refresh token, rafraîchissement. LinkedIn attend
// client_id / client_secret dans le CORPS form-urlencodé de l'endpoint de jeton (pas de
// Basic auth). Le refresh_token n'est délivré qu'aux applications habilitées par LinkedIn.
// Jetons rotationnés au coffre sous "social/linkedin".
// CLAUDE:WARN Aucun secret en dur ni env : client_id / client_secret / refresh_token lus
// via Vault.Use (zéro-out après usage). L'endpoint de jeton est INJECTABLE
// (Config.TokenURL → httptest), aucun appel LIVE n'atteint LinkedIn.
package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	// KeyClientID et KeyClientSecret sont les clés de coffre de l'application OAuth2.
	KeyClientID     = "client_id"
	KeyClientSecret = "client_secret"
	// KeyRefreshToken est la clé de coffre du jeton de rafraîchissement OAuth2.
	KeyRefreshToken = "refresh_token"

	// defaultTokenURL et defaultAuthorizeURL sont les endpoints OAuth2 de LinkedIn
	// (production). Surchargés en test via Config.TokenURL / Config.AuthorizeURL.
	defaultTokenURL     = "https://www.linkedin.com/oauth/v2/accessToken"
	defaultAuthorizeURL = "https://www.linkedin.com/oauth/v2/authorization"
)

// DefaultScopes sont les portées demandées : publication au nom du membre
// (w_member_social) et au nom d'une organisation (w_organization_social, qui exige
// l'habilitation Partner Program). r_organization_social autorise la relecture.
var DefaultScopes = []string{"w_member_social", "w_organization_social"}

// ErrNoRefreshToken est retourné quand un rafraîchissement est sollicité sans
// refresh_token au coffre (le flux d'autorisation initial n'a pas eu lieu, ou
// l'application n'est pas habilitée par LinkedIn à délivrer des refresh_token).
var ErrNoRefreshToken = errors.New("linkedin: aucun refresh_token au coffre")

// AuthorizeURL construit l'URL d'autorisation OAuth2 (response_type=code, scopes, state).
// Le client_id est lu au coffre. Aucune requête réseau n'est émise ici : l'usager est
// redirigé vers cette URL. LinkedIn n'impose pas PKCE pour le flux 3-legged confidentiel.
func (c *Connector) AuthorizeURL(ctx context.Context, state string) (string, error) {
	if strings.TrimSpace(c.redirect) == "" {
		return "", fmt.Errorf("linkedin.AuthorizeURL: RedirectURI requis")
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
	return c.authorizeURL + "?" + q.Encode(), nil
}

// ExchangeCode échange le code d'autorisation (grant_type=authorization_code) contre un
// access_token (et un refresh_token si l'application est habilitée), puis rotationne les
// jetons au coffre. Retourne l'access_token.
func (c *Connector) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirect)
	return c.tokenRequest(ctx, form)
}

// RefreshToken échange le refresh_token courant (grant_type=refresh_token) contre un
// nouveau couple access/refresh token, puis rotationne au coffre. Retourne le nouvel
// access_token. Disponible uniquement pour les applications habilitées par LinkedIn.
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

// tokenRequest exécute un POST vers l'endpoint de jeton OAuth2. LinkedIn attend client_id
// et client_secret dans le CORPS form-urlencodé (pas de Basic auth), désérialise la
// réponse et rotationne access_token (+ refresh_token si fourni) au coffre.
func (c *Connector) tokenRequest(ctx context.Context, form url.Values) (string, error) {
	clientID, err := c.vaultGet(ctx, KeyClientID)
	if err != nil {
		return "", err
	}
	clientSecret, err := c.vaultGet(ctx, KeyClientSecret)
	if err != nil {
		return "", err
	}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("linkedin.tokenRequest requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("linkedin.tokenRequest appel: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("linkedin.tokenRequest lecture réponse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("linkedin.tokenRequest HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		AccessToken           string `json:"access_token"`
		RefreshToken          string `json:"refresh_token"`
		TokenType             string `json:"token_type"`
		ExpiresIn             int    `json:"expires_in"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Scope                 string `json:"scope"`
		Error                 string `json:"error"`
		ErrorDesc             string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("linkedin.tokenRequest décodage: %w", err)
	}
	if tok.Error != "" {
		return "", fmt.Errorf("linkedin.tokenRequest erreur %s: %s", tok.Error, tok.ErrorDesc)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("linkedin.tokenRequest: réponse sans access_token")
	}

	// Set (upsert) : à l'échange initial, les jetons n'existent pas encore au coffre.
	if err := c.vault.Set(ctx, VaultConnectorID, KeyAccessToken, tok.AccessToken, ""); err != nil {
		return "", fmt.Errorf("linkedin.tokenRequest stockage access_token: %w", err)
	}
	if tok.RefreshToken != "" {
		if err := c.vault.Set(ctx, VaultConnectorID, KeyRefreshToken, tok.RefreshToken, ""); err != nil {
			return "", fmt.Errorf("linkedin.tokenRequest stockage refresh_token: %w", err)
		}
	}
	return tok.AccessToken, nil
}
