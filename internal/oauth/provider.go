// CLAUDE:SUMMARY OAuth 2.1 provider factory : NewProvider + NewOpenIDProvider via zitadel/oidc/v3/op (M-ASSOKIT-OAUTH-1).
package oauth

import (
	"crypto/sha256"
	"database/sql"
	"log/slog"
	"net/http"
	"os"

	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// NewProvider crée le Provider OIDC avec le Storage SQLite HS256.
// issuer doit être l'URL publique de l'instance (ex: "https://nps.example.com").
// signingKey est la clé secrète (COOKIE_SECRET ou OAUTH_SIGNING_KEY).
func NewProvider(db *sql.DB, issuer string, signingKey []byte, rbacStore *rbac.Store) (http.Handler, *Storage, error) {
	if os.Getenv("OAUTH_SIGNING_KEY") == "" {
		slog.Warn("OAUTH_SIGNING_KEY absent — tokens OAuth invalidés au restart, utiliser COOKIE_SECRET comme fallback")
	}

	store := New(db, signingKey, rbacStore)

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

	opts := []op.Option{
		op.WithAllowInsecure(), // HTTP autorisé en dev/staging (TLS géré par reverse proxy)
	}

	provider, err := op.NewOpenIDProvider(issuer, cfg, store, opts...)
	if err != nil {
		return nil, nil, err
	}

	return provider, store, nil
}
