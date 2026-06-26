// CLAUDE:SUMMARY PKCE S256 verify pour OAuth 2.1 (M-ASSOKIT-DCR-5).
// CLAUDE:WARN S256 = SHA256(verifier) base64url-encoded sans padding == challenge stocké.
package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
)

// Erreurs PKCE.
var (
	ErrPKCEMissingVerifier   = errors.New("pkce: code_verifier requis")
	ErrPKCEMissingChallenge  = errors.New("pkce: code_challenge stocké absent (auth code créé sans PKCE)")
	ErrPKCEMethodUnsupported = errors.New("pkce: code_challenge_method non supporté")
	ErrPKCEMismatch          = errors.New("pkce: code_verifier ne match pas code_challenge")
)

// VerifyPKCE compare code_verifier (fourni par le client à /oauth2/token)
// avec code_challenge stocké (depuis /oauth2/authorize).
//
// S256 UNIQUEMENT (RFC 7636) : challenge == base64url-encode(SHA256(verifier))
// sans padding. La méthode "plain" est REFUSÉE (le metadata OAuth annonce
// S256-only ; accepter plain rouvrirait une PKCE downgrade attack).
//
// Empty challenge : retourne ErrPKCEMissingChallenge (caller doit décider si
// PKCE est obligatoire pour ce client public — cf DCR-3 authorize).
func VerifyPKCE(verifier, challenge, method string) error {
	if challenge == "" {
		return ErrPKCEMissingChallenge
	}
	if verifier == "" {
		return ErrPKCEMissingVerifier
	}
	switch method {
	case "S256", "":
		// "" = default S256 selon RFC 7636 (méthode default si absent).
		hash := sha256.Sum256([]byte(verifier))
		expected := base64.RawURLEncoding.EncodeToString(hash[:])
		// Comparaison constant-time (évite un timing oracle sur le challenge).
		if subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) != 1 {
			return ErrPKCEMismatch
		}
		return nil
	default:
		// "plain" et toute autre méthode → refus explicite.
		return ErrPKCEMethodUnsupported
	}
}

// ChallengeFromVerifierS256 helper utilisé par tests E2E pour générer le
// code_challenge à partir du code_verifier (côté client claude.ai web).
func ChallengeFromVerifierS256(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
