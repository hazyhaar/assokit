// CLAUDE:SUMMARY /.well-known/oauth-authorization-server — RFC 8414 metadata enrichi DCR + PKCE (M-ASSOKIT-DCR-1).
// CLAUDE:WARN Cette route DOIT être enregistrée AVANT mountMCPEndpoint (qui définit aussi le même path) pour shadow l'ancienne version.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
)

// WellKnownOAuthAuthorizationServer GET /.well-known/oauth-authorization-server.
// RFC 8414 + extensions RFC 7591 (registration_endpoint) + PKCE (code_challenge_methods).
// Endpoint PUBLIC requis pour discovery par claude.ai web et autres clients MCP standards.
func WellKnownOAuthAuthorizationServer(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimRight(deps.Config.BaseURL, "/")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"issuer":                                base,
			"authorization_endpoint":                base + "/oauth2/authorize",
			"token_endpoint":                        base + "/oauth2/token",
			"registration_endpoint":                 base + "/oauth2/register",
			"jwks_uri":                              base + "/oauth2/jwks",
			"scopes_supported":                      []string{"openid", "profile", "mcp", "feedback.create", "feedback.list", "forum.post.create"},
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		})
	}
}
