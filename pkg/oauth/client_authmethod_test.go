package oauth

import (
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// TestAuthMethodPublicClient garde le cœur du fix OAuth audité 2026-06-13 : un
// client public (aucun secret enregistré via DCR token_endpoint_auth_method=none)
// doit annoncer AuthMethodNone. Sans cela (a) son échange de jeton est rejeté
// — la librairie hache "" et le compare au hash vide — cassant le connecteur
// claude.ai, et (b) l'exigence PKCE native de la librairie (réservée à None)
// n'est jamais appliquée.
func TestAuthMethodPublicClient(t *testing.T) {
	for _, tc := range []struct {
		name       string
		secretHash string
		want       oidc.AuthMethod
	}{
		{"client public (secret vide)", "", oidc.AuthMethodNone},
		{"client confidentiel", "sha256deadbeef", oidc.AuthMethodBasic},
	} {
		c := &oauthClient{id: "c", secretHash: tc.secretHash}
		if got := c.AuthMethod(); got != tc.want {
			t.Errorf("%s: AuthMethod() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
