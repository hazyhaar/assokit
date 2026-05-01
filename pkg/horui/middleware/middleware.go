// CLAUDE:SUMMARY Middleware HTTP NPS : Theme(cache30s), HTMX, Flash(ctx+cookie), Auth(HMAC cookie), RequirePerm, CSRF.
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

type ctxKey int

const (
	ctxKeyTheme ctxKey = iota
	ctxKeyHTMX
	ctxKeyFlash
	ctxKeyUser
	ctxKeyCSRF
)

// --- Theme ---

type themeCache struct {
	mu      sync.Mutex
	t       theme.Theme
	loadedAt time.Time
}

var globalThemeCache themeCache

// Theme charge le theme depuis SQLite (cache 30s) et l'injecte dans ctx.
func Theme(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			globalThemeCache.mu.Lock()
			if time.Since(globalThemeCache.loadedAt) > 30*time.Second {
				if t, err := theme.Load(db); err == nil {
					globalThemeCache.t = t
					globalThemeCache.loadedAt = time.Now()
				} else if globalThemeCache.loadedAt.IsZero() {
					globalThemeCache.t = theme.Defaults()
					globalThemeCache.loadedAt = time.Now()
				}
			}
			t := globalThemeCache.t
			globalThemeCache.mu.Unlock()
			ctx := context.WithValue(r.Context(), ctxKeyTheme, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ThemeFromContext extrait le theme du contexte.
func ThemeFromContext(ctx context.Context) theme.Theme {
	if t, ok := ctx.Value(ctxKeyTheme).(theme.Theme); ok {
		return t
	}
	return theme.Defaults()
}

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
		if c, err := r.Cookie("nps_flash"); err == nil {
			if data, err := base64.StdEncoding.DecodeString(c.Value); err == nil {
				json.Unmarshal(data, &msgs) //nolint:errcheck
			}
			http.SetCookie(w, &http.Cookie{Name: "nps_flash", MaxAge: -1, Path: "/"})
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
		Name:     "nps_flash",
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

const sessionCookieName = "nps_session"

// Auth lit le cookie session, charge l'utilisateur depuis DB et l'injecte dans ctx.
func Auth(db *sql.DB, secret []byte) func(http.Handler) http.Handler {
	store := &auth.Store{DB: db}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var u *auth.User
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
func UserFromContext(ctx context.Context) *auth.User {
	u, _ := ctx.Value(ctxKeyUser).(*auth.User)
	return u
}

// ContextWithUser injecte un utilisateur dans le contexte. Réservé aux tests.
func ContextWithUser(ctx context.Context, u *auth.User) context.Context {
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

// ClearSessionCookie supprime le cookie de session.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
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
	if signHMAC(secret, payload) != sig {
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

const csrfCookieName = "nps_csrf"
const csrfHeaderName = "X-CSRF-Token"
const csrfFieldName = "_csrf"

// CSRF middleware double-submit cookie pattern.
func CSRF(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ensureCSRFToken(w, r)
			ctx := context.WithValue(r.Context(), ctxKeyCSRF, token)
			r = r.WithContext(ctx)

			method := strings.ToUpper(r.Method)
			if method == "POST" || method == "PUT" || method == "DELETE" || method == "PATCH" {
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

func ensureCSRFToken(w http.ResponseWriter, r *http.Request) string {
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
		SameSite: http.SameSiteLaxMode,
	})
	return token
}
