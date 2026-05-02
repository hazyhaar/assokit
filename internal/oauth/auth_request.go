// CLAUDE:SUMMARY op.AuthRequest SQLite — authRequest struct + compile-time assertion (M-ASSOKIT-OAUTH-1).
package oauth

import (
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var _ op.AuthRequest = (*authRequest)(nil)

type authRequest struct {
	id            string
	clientID      string
	userID        string
	scopes        []string
	redirectURI   string
	nonce         string
	state         string
	codeChallenge *oidc.CodeChallenge
	responseType  oidc.ResponseType
	authTime      time.Time
	done          bool
}

func (r *authRequest) GetID() string                         { return r.id }
func (r *authRequest) GetACR() string                        { return "" }
func (r *authRequest) GetAMR() []string                      { return nil }
func (r *authRequest) GetAudience() []string                 { return []string{r.clientID} }
func (r *authRequest) GetAuthTime() time.Time                { return r.authTime }
func (r *authRequest) GetClientID() string                   { return r.clientID }
func (r *authRequest) GetCodeChallenge() *oidc.CodeChallenge { return r.codeChallenge }
func (r *authRequest) GetNonce() string                      { return r.nonce }
func (r *authRequest) GetRedirectURI() string                { return r.redirectURI }
func (r *authRequest) GetResponseType() oidc.ResponseType    { return r.responseType }
func (r *authRequest) GetResponseMode() oidc.ResponseMode    { return "" }
func (r *authRequest) GetScopes() []string                   { return r.scopes }
func (r *authRequest) GetState() string                      { return r.state }
func (r *authRequest) GetSubject() string                    { return r.userID }
func (r *authRequest) Done() bool                            { return r.done }
