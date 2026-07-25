// CLAUDE:SUMMARY Middleware HTTP assokit : HTMX, Flash(ctx+cookie), Auth(HMAC cookie), RequirePerm, CSRF.
package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
)

type ctxKey int

const (
	ctxKeyHTMX ctxKey = iota
	ctxKeyFlash
	ctxKeyUser
	ctxKeyCSRF
)

// --- HTMX ---

// HTMX détecte HX-Request et injecte dans ctx.
func HTMX(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isHX := r.Header.Get("HX-Request") == "true"
		ctx := context.WithValue(r.Context(), ctxKeyHTMX, isHX)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// IsHTMX retourne true si la requête est une requête HTMX.
func IsHTMX(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyHTMX).(bool)
	return v
}

// --- Flash ---

// FlashMessage représente un message flash.
type FlashMessage struct {
	Level   string
	Message string
}

// Flash middleware stocke les messages flash en ctx.
func Flash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []FlashMessage
		if c, err := r.Cookie("assokit_flash"); err == nil {
			if data, err := base64.StdEncoding.DecodeString(c.Value); err == nil {
				json.Unmarshal(data, &msgs) //nolint:errcheck
			}
			http.SetCookie(w, &http.Cookie{Name: "assokit_flash", MaxAge: -1, Path: "/"})
		}
		ctx := context.WithValue(r.Context(), ctxKeyFlash, msgs)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PushFlash ajoute un message flash (persisté dans cookie sur la réponse suivante).
func PushFlash(w http.ResponseWriter, level, msg string) {
	msgs := []FlashMessage{{Level: level, Message: msg}}
	data, _ := json.Marshal(msgs)
	http.SetCookie(w, &http.Cookie{
		Name:     "assokit_flash",
		Value:    base64.StdEncoding.EncodeToString(data),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

// PopFlash retourne les messages flash du contexte.
func PopFlash(ctx context.Context) []FlashMessage {
	msgs, _ := ctx.Value(ctxKeyFlash).([]FlashMessage)
	return msgs
}

// --- Auth ---

const sessionCookieName = "assokit_session"

// Auth lit le cookie session, charge l'utilisateur depuis DB et l'injecte dans ctx.
func Auth(db *sql.DB, secret []byte) func(http.Handler) http.Handler {
	store := &identity.Store{DB: db}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var u *identity.User
			if c, err := r.Cookie(sessionCookieName); err == nil {
				if id, ok := verifySession(c.Value, secret); ok {
					ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
					u, _ = store.GetByID(ctx, id)
					cancel()
				}
			}
			ctx := context.WithValue(r.Context(), ctxKeyUser, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retourne l'utilisateur courant (nil si non connecté).
func UserFromContext(ctx context.Context) *identity.User {
	u, _ := ctx.Value(ctxKeyUser).(*identity.User)
	return u
}

// RequireMetierGrade exige que l'utilisateur authentifié détienne le grade nommé
// (présent dans u.Roles). Non authentifié → redirect login ; authentifié sans
// grade → 403. Exporté pour les routes membre montées par la bordure d'instance.
func RequireMetierGrade(grade string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				http.Redirect(w, r, "/login?redirect_uri="+url.QueryEscape(r.URL.Path), http.StatusFound)
				return
			}
			if !slices.Contains(u.Roles, grade) {
				http.Error(w, "Accès refusé", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ContextWithUser injecte un utilisateur dans le contexte. Réservé aux tests.
func ContextWithUser(ctx context.Context, u *identity.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// SetSessionCookie pose le cookie de session signé.
func SetSessionCookie(w http.ResponseWriter, userID string, secret []byte, secure bool) {
	expires := time.Now().Add(7 * 24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", userID, expires)
	sig := signHMAC(secret, payload)
	value := base64.StdEncoding.EncodeToString([]byte(payload + ":" + sig))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
}

// ClearSessionCookie supprime le cookie de session côté navigateur.
//
// Limite assumée : la session est sans état (cookie signé HMAC, aucune liste de
// révocation serveur). Le logout efface le cookie du navigateur mais ne révoque
// pas un cookie déjà capturé, qui reste valide jusqu'à son expiration (7 jours).
// Acceptable pour le modèle « une instance = une communauté » ; introduire un
// identifiant de session opaque persisté serait le prix d'une révocation immédiate.
// La suppression matche sur name/path/domain (RFC 6265), indépendamment des
// attributs Secure/SameSite.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func verifySession(cookieVal string, secret []byte) (string, bool) {
	data, err := base64.StdEncoding.DecodeString(cookieVal)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(data), ":", 3)
	if len(parts) != 3 {
		return "", false
	}
	userID, expiresStr, sig := parts[0], parts[1], parts[2]
	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return "", false
	}
	payload := userID + ":" + expiresStr
	if !hmac.Equal([]byte(signHMAC(secret, payload)), []byte(sig)) {
		return "", false
	}
	return userID, true
}

func signHMAC(secret []byte, payload string) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// --- RequirePerm ---

// RequirePerm retourne 403 si l'utilisateur n'a pas la permission requise sur le node.
func RequirePerm(db *sql.DB, p perms.Permission, nodeIDFn func(*http.Request) string) func(http.Handler) http.Handler {
	ps := &perms.Store{DB: db}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if u == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			nodeID := nodeIDFn(r)
			can, err := ps.UserCan(r.Context(), u.Roles, nodeID, p)
			if err != nil || !can {
				http.Error(w, "Accès refusé", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- CSRF ---

const csrfCookieName = "assokit_csrf"
const csrfHeaderName = "X-CSRF-Token"
const csrfFieldName = "_csrf"

// csrfExemptPrefixes : paths où le CSRF check est désactivé.
// /webhooks/* : POST depuis serveurs tiers (HelloAsso, Stripe, etc.) sans cookie CSRF.
//
//	Sécurité = HMAC signature provider-specific (verify dans handler).
var csrfExemptPrefixes = []string{
	"/webhooks/",
}

// CSRF middleware double-submit cookie pattern. Exempte /webhooks/* (HMAC-protected).
// secure : pose l'attribut Secure sur le cookie CSRF (dérivé de la config HTTPS à la
// bordure), pour qu'il ne transite pas en clair sur un downgrade HTTP.
func CSRF(secret []byte, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ensureCSRFToken(w, r, secure)
			ctx := context.WithValue(r.Context(), ctxKeyCSRF, token)
			r = r.WithContext(ctx)

			method := strings.ToUpper(r.Method)
			if method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH" {
				// Exempt /webhooks/* du CSRF check (HelloAsso/Stripe POST sans cookie).
				for _, prefix := range csrfExemptPrefixes {
					if strings.HasPrefix(r.URL.Path, prefix) {
						next.ServeHTTP(w, r)
						return
					}
				}
				formToken := r.FormValue(csrfFieldName)
				if formToken == "" {
					formToken = r.Header.Get(csrfHeaderName)
				}
				if !hmac.Equal([]byte(token), []byte(formToken)) {
					http.Error(w, "CSRF token invalide", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFToken retourne le token CSRF du contexte.
func CSRFToken(ctx context.Context) string {
	t, _ := ctx.Value(ctxKeyCSRF).(string)
	return t
}

func ensureCSRFToken(w http.ResponseWriter, r *http.Request, secure bool) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	token := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // doit être lisible par JS si besoin
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}
