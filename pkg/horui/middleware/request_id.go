// CLAUDE:SUMMARY Middleware RequestID — génère UUID v4 par requête, injecte ctx + header X-Request-ID (M-ASSOKIT-AUDIT-FIX-1).
// CLAUDE:WARN Doit être monté EN PREMIER dans la chaîne (avant Auth/CSRF) pour que tous les slogs en aval puissent récupérer le req_id.
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"
)

type requestIDKey struct{}

// RequestID middleware : génère un UUID v4 par requête, l'injecte dans ctx + response header.
// Le header X-Request-ID entrant n'est jamais utilisé (trust serveur uniquement, pas client).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.New().String()
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext récupère le req_id injecté par le middleware RequestID.
// Retourne "" si absent (handler hors chaîne middleware ou test sans middleware).
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithRequestID injecte un req_id explicite dans le contexte. Réservé aux tests
// et à l'instrumentation interne (ex: jobs background sans HTTP request).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// HashEmail retourne un hash SHA256 court (16 chars hex) d'un email pour les logs.
// Permet de tracer un user dans les logs sans exposer le PII.
func HashEmail(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:8])
}

// HashIP retourne un hash SHA256 court (16 chars hex) d'une IP avec un secret.
// Permet de corréler des requêtes sans exposer l'IP brute (RGPD).
func HashIP(ip string, secret []byte) string {
	h := sha256.New()
	h.Write([]byte(ip))
	h.Write(secret)
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// SecurityHeaders middleware : pose les en-têtes OWASP de base sur toutes les réponses.
// X-Frame-Options DENY : refuse iframe (clickjacking).
// X-Content-Type-Options nosniff : refuse MIME sniffing (XSS).
// Referrer-Policy : limite l'exfiltration cross-origin.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
