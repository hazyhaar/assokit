// CLAUDE:SUMMARY Rafraîchissement actif du Page Access Token Meta — échange
// fb_exchange_token (long-lived) contre la racine Graph injectée, puis rotation au coffre.
// CLAUDE:WARN Aucun secret en dur ni env : app_secret et jeton courant lus via
// Vault.Use (zéro-out après usage). Désactivé si Config.AppID est vide.
package meta

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

// ErrRefreshDisabled est retourné quand l'échange de jeton est sollicité sans AppID
// configuré (rafraîchissement actif désactivé : le jeton du coffre est utilisé tel quel).
var ErrRefreshDisabled = errors.New("meta: rafraîchissement de jeton désactivé (AppID absent)")

// RefreshLongLivedToken échange le Page Access Token courant contre un jeton
// long-lived via le grant fb_exchange_token de l'API Graph, puis ROTATIONNE le
// nouveau jeton au coffre (clé page_access_token). Retourne le nouveau jeton.
//
// Contrat Graph : GET {graph}/{version}/oauth/access_token?grant_type=fb_exchange_token
// &client_id={app_id}&client_secret={app_secret}&fb_exchange_token={current}.
func (c *Connector) RefreshLongLivedToken(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.cfg.AppID) == "" {
		return "", ErrRefreshDisabled
	}

	var current string
	if err := c.vault.Use(ctx, VaultConnectorID, KeyPageAccessToken, func(pt string) error {
		current = pt
		return nil
	}); err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken jeton courant: %w", err)
	}
	var secret string
	if err := c.vault.Use(ctx, VaultConnectorID, KeyAppSecret, func(pt string) error {
		secret = pt
		return nil
	}); err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken app_secret: %w", err)
	}

	// Secrets (app_secret, fb_exchange_token) transmis dans le CORPS form-urlencodé,
	// jamais en query string : une erreur de transport (*url.Error) inclut l'URL mais
	// pas le corps, ce qui évite de fuiter les secrets dans un log d'appelant.
	form := url.Values{}
	form.Set("grant_type", "fb_exchange_token")
	form.Set("client_id", c.cfg.AppID)
	form.Set("client_secret", secret)
	form.Set("fb_exchange_token", current)
	endpoint := c.graph + "/" + c.version + "/oauth/access_token"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken requête: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken appel Graph: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken lecture réponse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("meta.RefreshLongLivedToken Graph HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken décodage: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("meta.RefreshLongLivedToken: réponse sans access_token")
	}

	if err := c.vault.Rotate(ctx, VaultConnectorID, KeyPageAccessToken, tok.AccessToken, ""); err != nil {
		return "", fmt.Errorf("meta.RefreshLongLivedToken rotation coffre: %w", err)
	}
	return tok.AccessToken, nil
}
