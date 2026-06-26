// CLAUDE:SUMMARY Shim interne : ré-exporte les symboles pkg/oauth sous l'ancienne API internal/oauth (M-ASSOKIT-OAUTH-1).
// Objectif : rétro-compatibilité des handlers et tests assokit sans modification d'appel.
// Les consommateurs externes doivent importer pkg/oauth directement.
package oauth

import (
	"context"
	"database/sql"
	"time"

	pkgoauth "github.com/hazyhaar/assokit/pkg/oauth"
)

// ─── types ré-exportés ────────────────────────────────────────────────────────

// RegisterRequest : RFC 7591 client registration request.
type RegisterRequest = pkgoauth.RegisterRequest

// RegisterResponse : RFC 7591 client registration response.
type RegisterResponse = pkgoauth.RegisterResponse

// ─── erreurs ré-exportées ─────────────────────────────────────────────────────

var (
	ErrNotFound     = pkgoauth.ErrNotFound
	ErrCodeUsed     = pkgoauth.ErrCodeUsed
	ErrTokenRevoked = pkgoauth.ErrTokenRevoked

	ErrInvalidRedirectURI = pkgoauth.ErrInvalidRedirectURI
	ErrMissingRedirectURI = pkgoauth.ErrMissingRedirectURI
	ErrInvalidGrantType   = pkgoauth.ErrInvalidGrantType
	ErrInvalidAuthMethod  = pkgoauth.ErrInvalidAuthMethod

	ErrPKCEMissingVerifier   = pkgoauth.ErrPKCEMissingVerifier
	ErrPKCEMissingChallenge  = pkgoauth.ErrPKCEMissingChallenge
	ErrPKCEMethodUnsupported = pkgoauth.ErrPKCEMethodUnsupported
	ErrPKCEMismatch          = pkgoauth.ErrPKCEMismatch
)

// DCRClientTTL ré-exportée.
const DCRClientTTL = pkgoauth.DCRClientTTL

// ─── fonctions ré-exportées ──────────────────────────────────────────────────

// RandomToken ré-exporte pkg/oauth.RandomToken.
func RandomToken() (string, error) {
	return pkgoauth.RandomToken()
}

// ValidateRedirectURI ré-exporte pkg/oauth.ValidateRedirectURI.
func ValidateRedirectURI(uri string) error {
	return pkgoauth.ValidateRedirectURI(uri)
}

// VerifyPKCE ré-exporte pkg/oauth.VerifyPKCE.
func VerifyPKCE(verifier, challenge, method string) error {
	return pkgoauth.VerifyPKCE(verifier, challenge, method)
}

// ChallengeFromVerifierS256 ré-exporte pkg/oauth.ChallengeFromVerifierS256.
func ChallengeFromVerifierS256(verifier string) string {
	return pkgoauth.ChallengeFromVerifierS256(verifier)
}

// Register crée un nouveau client OAuth DCR en utilisant uidGen implicite.
// Signature inchangée pour la rétro-compatibilité (sans paramètre IDGenerator).
func Register(ctx context.Context, db *sql.DB, req RegisterRequest) (*RegisterResponse, error) {
	return pkgoauth.Register(ctx, db, req, uidGen{})
}

// PurgeStaleClients ré-exporte pkg/oauth.PurgeStaleClients.
func PurgeStaleClients(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	return pkgoauth.PurgeStaleClients(ctx, db, now)
}
