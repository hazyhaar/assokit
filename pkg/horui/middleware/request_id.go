// CLAUDE:SUMMARY Middleware RequestID — génère UUID v4 par requête, injecte ctx + header X-Request-ID (M-ASSOKIT-AUDIT-FIX-1).
// CLAUDE:WARN Doit être monté EN PREMIER dans la chaîne (avant Auth/CSRF) pour que tous les slogs en aval puissent récupérer le req_id.
package middleware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/hazyhaar/assokit/pkg/uid"
)

type requestIDKey struct{}

type nonceKey struct{}

// RequestID middleware : génère un UUID v4 par requête, l'injecte dans ctx + response header.
// Le header X-Request-ID entrant n'est jamais utilisé (trust serveur uniquement, pas client).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uid.New()
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

// generateNonce produce un nonce aléatoire base64 (18 octets = 24 chars) pour CSP.
func generateNonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// NonceFromContext récupère le nonce CSP injecté par SecurityHeadersWith.
func NonceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(nonceKey{}).(string)
	return v
}

// WithNonce injecte un nonce CSP explicite dans le contexte. Réservé aux tests.
func WithNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, nonceKey{}, nonce)
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

// CSPExtra porte des sources additionnelles autorisées PAR DIRECTIVE, injectées à
// la bordure d'instance (le core reste durci par défaut). Une valeur zéro (tous
// champs vides) ne modifie EN RIEN la CSP par défaut : l'instance qui n'injecte
// rien conserve exactement le durcissement historique. Chaque liste ajoute des
// sources (hôtes, schémas comme "blob:", mots-clés) à la directive correspondante,
// sans jamais retirer ni élargir les autres. WorkerSrc matérialise une directive
// worker-src explicite (absente du défaut) uniquement si des sources sont fournies
// (MapLibre GL instancie ses web workers depuis des URL blob:, bloquées par le
// repli sur default-src 'self').
type CSPExtra struct {
	ScriptSrc  []string
	StyleSrc   []string
	ConnectSrc []string
	ImgSrc     []string
	WorkerSrc  []string
}

// IsZero indique qu'aucune source additionnelle n'est fournie : la CSP rendue est
// alors octet-pour-octet la CSP par défaut durcie.
func (e CSPExtra) IsZero() bool {
	return len(e.ScriptSrc) == 0 && len(e.StyleSrc) == 0 && len(e.ConnectSrc) == 0 &&
		len(e.ImgSrc) == 0 && len(e.WorkerSrc) == 0
}

// cspDirective : une directive CSP ordonnée (value vide = directive sans valeur,
// ex. upgrade-insecure-requests).
type cspDirective struct{ name, value string }

// baseCSPDirectives est la CSP par défaut durcie, sous forme ordonnée. La
// directive script-src est complétée per-request avec un nonce (cf. buildCSP) :
// le nonce remplace 'unsafe-inline' pour contenir les scripts inline autorisés.
// style-src garde 'unsafe-inline' : les styles inline ( attributs style= ) sont
// défense-en-profondeur limités (pas d'exécution JS via style).
var baseCSPDirectives = []cspDirective{
	{"default-src", "'self'"},
	{"img-src", "'self' data: https:"},
	{"frame-src", "'self' https://*.helloasso.com https://www.helloasso.com"},
	{"script-src", "'self'"},
	{"style-src", "'self' 'unsafe-inline'"},
	{"font-src", "'self' data:"},
	{"connect-src", "'self'"},
	{"object-src", "'none'"},
	{"base-uri", "'self'"},
	{"form-action", "'self' https://*.helloasso.com"},
	{"frame-ancestors", "'none'"},
	{"upgrade-insecure-requests", ""},
}

// buildCSPWithNonce sérialise la CSP par défaut augmentée des sources additionnelles
// et d'un nonce pour script-src. Le nonce remplace 'unsafe-inline' : seuls les
// scripts portant le nonce (inline) ou provenant de 'self' (externe) sont autorisés.
// 'strict-dynamic' permet aux scripts nonceés de charger dynamiquement d'autres scripts.
func buildCSPWithNonce(extra CSPExtra, nonce string) string {
	dirs := make([]cspDirective, len(baseCSPDirectives))
	copy(dirs, baseCSPDirectives)

	// Injecter le nonce dans script-src : 'self' 'nonce-{nonce}' 'strict-dynamic'
	if nonce != "" {
		for i := range dirs {
			if dirs[i].name == "script-src" {
				dirs[i].value = "'self' 'nonce-" + nonce + "' 'strict-dynamic'"
			}
		}
	}

	appendSrc := func(name string, srcs []string) {
		if len(srcs) == 0 {
			return
		}
		for i := range dirs {
			if dirs[i].name == name {
				dirs[i].value += " " + strings.Join(srcs, " ")
				return
			}
		}
		// Directive absente du défaut (ex. worker-src) : matérialisée avec 'self'
		// pour ne pas retomber sur default-src, plus les sources fournies.
		dirs = append(dirs, cspDirective{name, "'self' " + strings.Join(srcs, " ")})
	}
	appendSrc("script-src", extra.ScriptSrc)
	appendSrc("style-src", extra.StyleSrc)
	appendSrc("connect-src", extra.ConnectSrc)
	appendSrc("img-src", extra.ImgSrc)
	appendSrc("worker-src", extra.WorkerSrc)

	parts := make([]string, len(dirs))
	for i, d := range dirs {
		if d.value == "" {
			parts[i] = d.name
		} else {
			parts[i] = d.name + " " + d.value
		}
	}
	return strings.Join(parts, "; ")
}

// SecurityHeaders middleware : pose les en-têtes OWASP de base sur toutes les
// réponses, avec la CSP par défaut durcie (aucune source externe hors HelloAsso).
// Équivaut à SecurityHeadersWith(CSPExtra{}). Conservé pour les appelants sans
// extension.
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersWith(CSPExtra{})(next)
}

// SecurityHeadersWith fabrique le middleware d'en-têtes de sécurité en composant la
// CSP par défaut durcie PLUS les sources additionnelles fournies par la bordure.
// X-Frame-Options DENY : refuse iframe (clickjacking).
// X-Content-Type-Options nosniff : refuse MIME sniffing (XSS).
// Referrer-Policy : limite l'exfiltration cross-origin.
// Strict-Transport-Security : force HTTPS (posé seulement en HTTPS, sinon Chrome
// jette le header).
// Content-Security-Policy : cf. baseCSPDirectives (durcissement standard) augmentée
// des sources de extra (script/style/connect/img/worker) pour l'instance.
func SecurityHeadersWith(extra CSPExtra) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := generateNonce()
			ctx := WithNonce(r.Context(), nonce)
			csp := buildCSPWithNonce(extra, nonce)
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy", csp)
			// HSTS uniquement en HTTPS (Chrome ignore + warns sur HTTP).
			// 1 an, includeSubDomains. preload omis volontairement.
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
