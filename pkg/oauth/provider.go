// CLAUDE:SUMMARY OAuth 2.1 provider factory : NewProvider + NewOpenIDProvider via zitadel/oidc/v3/op (M-ASSOKIT-OAUTH-1).
package oauth

import (
	"crypto/sha256"
	"database/sql"
	"net/http"

	"github.com/zitadel/oidc/v3/pkg/op"
)

// NewProvider crée le Provider OIDC avec le Storage SQLite HS256.
// issuer doit être l'URL publique de l'instance (ex: "https://asso.example.com").
// signingKey est la clé secrète (COOKIE_SECRET ou OAUTH_SIGNING_KEY).
// userProvider fournit les données utilisateur (implémenté côté consommateur).
// idGen produit les identifiants OAuth (implémenté côté consommateur).
func NewProvider(db *sql.DB, issuer string, signingKey []byte, allowInsecure bool, userProvider UserProvider, idGen IDGenerator) (http.Handler, *Storage, error) {
	store := New(db, signingKey, userProvider, idGen)

	// CryptoKey [32]byte pour l'AES interne du provider (chiffrement token bearer).
	sum := sha256.Sum256(signingKey)
	var cryptoKey [32]byte
	copy(cryptoKey[:], sum[:])

	cfg := &op.Config{
		CryptoKey:             cryptoKey,
		GrantTypeRefreshToken: true,
		AuthMethodPost:        true,
		CodeMethodS256:        true,
		SupportedScopes: []string{
			"openid", "profile", "email", "offline_access",
		},
	}

	opts := []op.Option{}
	if allowInsecure {
		opts = append(opts, op.WithAllowInsecure())
	}

	provider, err := op.NewOpenIDProvider(issuer, cfg, store, opts...)
	if err != nil {
		return nil, nil, err
	}

	return provider, store, nil
}
