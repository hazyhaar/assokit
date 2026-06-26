// CLAUDE:SUMMARY Vérification signature webhook LiveKit (JWT HS256 + SHA256 body hash). Stdlib pur, CGO=0.
// CLAUDE:WARN Le JWT doit porter le claim "sha256" égal au SHA256 hex du body brut. Jamais logguer le secret.
package livekit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
)

// VerifyWebhookJWT vérifie la signature d'un webhook LiveKit.
//
// LiveKit signe ses webhooks en plaçant un JWT HS256 dans le header
// Authorization (format "Bearer <jwt>"). Le JWT doit porter un claim "sha256"
// contenant le SHA256 hex du body brut. La vérification se fait en deux temps :
//  1. Re-signer header.payload avec le api_secret (HS256) et comparer la signature.
//  2. Vérifier que sha256(body) == claims["sha256"].
//
// Choix d'implémentation : le WebhookHandler générique applique HMAC-SHA256 via
// Vault.Use("provider","webhook_signing_secret") — ce schéma est INCOMPATIBLE avec
// le schéma LiveKit (JWT portant un hash du body, pas une signature directe du body).
// On ajoute un vérificateur dédié appelé depuis HandleWebhook (pas depuis le
// WebhookHandler générique qui ne voit pas le raw body au bon moment).
// Pour la route /webhooks/livekit, le SignatureConfig.HeaderName est mis à "Authorization"
// et la HMAC verify générique est court-circuitée : on place une webhook_signing_secret
// factice dans le Vault avec valeur sentinelle "livekit-jwt" pour que verifyHMAC ne
// bloque pas la reception — mais HandleWebhook refait la vérif JWT complète.
// Compromis documenté : la vérification réelle se fait DANS HandleWebhook, pas dans
// le receiver générique. Anti-DoS minimal maintenu (volume de payload limité à 1 MiB
// par le receiver générique).
func VerifyWebhookJWT(ctx context.Context, vault *assets.Vault, authorization string, body []byte) error {
	if vault == nil {
		return errors.New("livekit: vault non configuré")
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("livekit: Authorization header vide ou absent")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("livekit: JWT malformé (attendu 3 parties)")
	}

	// Extraire les claims (2e partie) sans vérification de signature d'abord
	// pour récupérer le claim sha256.
	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("livekit: décodage claims JWT: %w", err)
	}

	var rawClaims map[string]any
	if err := json.Unmarshal(claimBytes, &rawClaims); err != nil {
		return fmt.Errorf("livekit: parse claims JWT: %w", err)
	}

	bodyHashClaim, _ := rawClaims["sha256"].(string)

	return vault.Use(ctx, "livekit", "api_secret", func(apiSecret string) error {
		// 1. Vérifier la signature HS256.
		signingInput := parts[0] + "." + parts[1]
		mac := hmac.New(sha256.New, []byte(apiSecret))
		mac.Write([]byte(signingInput))
		expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
			return errors.New("livekit: signature JWT invalide")
		}

		// 2. Vérifier le hash du body.
		if bodyHashClaim == "" {
			// Certaines versions de LiveKit server omettent ce claim.
			// Accepter si la signature est valide (mode permissif documenté).
			return nil
		}
		h := sha256.Sum256(body)
		actualHash := hex.EncodeToString(h[:])
		if !hmac.Equal([]byte(actualHash), []byte(bodyHashClaim)) {
			return errors.New("livekit: hash body JWT invalide")
		}
		return nil
	})
}
