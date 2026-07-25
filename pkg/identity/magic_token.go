// Jetons login_magic_tokens : connexion magic-link (15 min) et activation
// propriétaire (72 h — fenêtre de transmission manuelle par l'administrateur).
package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// LoginMagicTokenTTL : durée de validité d'un lien de connexion magic-link.
	LoginMagicTokenTTL = 15 * time.Minute
	// ActivationTokenTTL : durée de validité d'un lien d'activation propriétaire.
	// Plus long que LoginMagicTokenTTL pour permettre la transmission manuelle
	// lorsque le mailer est désactivé (risque résiduel : lien visible à l'admin).
	ActivationTokenTTL = 72 * time.Hour
)

// IssuedMagicToken décrit un jeton émis dans login_magic_tokens.
type IssuedMagicToken struct {
	Token     string
	ExpiresAt time.Time
}

// IssueMagicToken insère un jeton à usage unique dans login_magic_tokens.
func (s *Store) IssueMagicToken(ctx context.Context, email, userID, returnURL, ipHash string, ttl time.Duration) (*IssuedMagicToken, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("identity.IssueMagicToken: email invalide")
	}
	if returnURL == "" {
		returnURL = "/"
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("identity.IssueMagicToken rand: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().UTC().Add(ttl)

	var uid sql.NullString
	if userID != "" {
		uid = sql.NullString{String: userID, Valid: true}
	}

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO login_magic_tokens(token, email, user_id, return_url, expires_at, ip_hash)
		VALUES (?, ?, ?, ?, ?, ?)
	`, token, email, uid, returnURL, expiresAt.Format("2006-01-02 15:04:05"), ipHash)
	if err != nil {
		return nil, fmt.Errorf("identity.IssueMagicToken insert: %w", err)
	}

	return &IssuedMagicToken{Token: token, ExpiresAt: expiresAt}, nil
}
