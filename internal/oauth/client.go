// CLAUDE:SUMMARY op.Client SQLite — oauthClient struct + compile-time assertion (M-ASSOKIT-OAUTH-1).
package oauth

import (
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var _ op.Client = (*oauthClient)(nil)

type oauthClient struct {
	id           string
	secretHash   string
	redirectURIs []string
	grantTypes   []oidc.GrantType
	scopes       []string
}

func (c *oauthClient) GetID() string                       { return c.id }
func (c *oauthClient) RedirectURIs() []string              { return c.redirectURIs }
func (c *oauthClient) PostLogoutRedirectURIs() []string    { return nil }
func (c *oauthClient) ApplicationType() op.ApplicationType { return op.ApplicationTypeWeb }
func (c *oauthClient) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodBasic }
func (c *oauthClient) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c *oauthClient) GrantTypes() []oidc.GrantType        { return c.grantTypes }
func (c *oauthClient) LoginURL(id string) string           { return "/oauth2/consent?id=" + id }
func (c *oauthClient) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeBearer }
func (c *oauthClient) IDTokenLifetime() time.Duration      { return time.Hour }
func (c *oauthClient) DevMode() bool                       { return false }
func (c *oauthClient) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *oauthClient) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *oauthClient) IsScopeAllowed(scope string) bool {
	for _, s := range c.scopes {
		if s == scope {
			return true
		}
	}
	return scope == oidc.ScopeOpenID || scope == oidc.ScopeOfflineAccess || scope == oidc.ScopeProfile || scope == oidc.ScopeEmail
}
func (c *oauthClient) IDTokenUserinfoClaimsAssertion() bool { return false }
func (c *oauthClient) ClockSkew() time.Duration             { return 0 }
