// CLAUDE:SUMMARY HelloAsso OAuth2 client_credentials flow + token refresh + Vault.Use lazy load (M-ASSOKIT-SPRINT3-S1).
// CLAUDE:WARN client_secret accédé via vault.Use uniquement (callback ZeroBytes après). Token endpoint switch sandbox/prod.
package helloasso

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
)

const (
	// TokenURLProd : endpoint OAuth2 production HelloAsso.
	TokenURLProd = "https://api.helloasso.com/oauth2/token"
	// TokenURLSandbox : endpoint OAuth2 sandbox HelloAsso.
	TokenURLSandbox = "https://api.helloasso-sandbox.com/oauth2/token"
	// APIBaseProd : base URL API v5 production.
	APIBaseProd = "https://api.helloasso.com"
	// APIBaseSandbox : base URL API v5 sandbox.
	APIBaseSandbox = "https://api.helloasso-sandbox.com"
)

// TokenURLFor retourne l'endpoint OAuth2 selon sandbox flag.
func TokenURLFor(sandbox bool) string {
	if sandbox {
		return TokenURLSandbox
	}
	return TokenURLProd
}

// APIBaseFor retourne la base API v5 selon sandbox flag.
func APIBaseFor(sandbox bool) string {
	if sandbox {
		return APIBaseSandbox
	}
	return APIBaseProd
}

// NewOAuthHTTPClient construit un *http.Client OAuth2 qui s'auto-refresh.
// client_secret est lu via vault.Use (synchrone), puis stocké dans la
// clientcredentials.Config qui le réutilisera en mémoire pour chaque refresh.
//
// Trade-off : oauth2/clientcredentials ne supporte pas un secret callback
// dynamique — il faut stocker la chaîne. La défense en profondeur reste :
// (1) jamais loggué, (2) jamais exposé via API admin, (3) ZeroBytes au Stop.
func NewOAuthHTTPClient(ctx context.Context, vault *assets.Vault, connectorID, clientID, tokenURL string) (*http.Client, oauth2.TokenSource, error) {
	var secret string
	if err := vault.Use(ctx, connectorID, "client_secret", func(plaintext string) error {
		secret = plaintext
		return nil
	}); err != nil {
		return nil, nil, err
	}

	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		TokenURL:     tokenURL,
	}
	src := cfg.TokenSource(ctx)
	cli := oauth2.NewClient(ctx, src)
	return cli, src, nil
}
