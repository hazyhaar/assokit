// CLAUDE:SUMMARY Magic link login zéro-password (M-ASSOKIT-DCR-2).
// CLAUDE:WARN Token expiry 15min, single-use via used_at. Rate-limit 3/15min/IP. ip_hash stocké, pas IP brute (RGPD).
package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
)

// _ = auth pour éviter import unused si certains chemins removed.
var _ = "magic_link" // sentinel pour clarté package

const (
	magicRateLimit  = 3
	magicRateWindow = 15 * time.Minute
)

// magicRateLimiter : 3 demandes/15min/IP. Pattern identique à dcrRateLimiter.
//
// Limite assumée (H3) : in-memory, non partagé entre instances. Voir login_guard.go.
type magicRateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
}

func newMagicRateLimiter() *magicRateLimiter {
	return &magicRateLimiter{buckets: make(map[string][]time.Time)}
}

func (r *magicRateLimiter) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-magicRateWindow)
	stamps := r.buckets[ip]
	kept := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= magicRateLimit {
		r.buckets[ip] = kept
		return false
	}
	kept = append(kept, now)
	r.buckets[ip] = kept
	return true
}

var globalMagicRateLimiter = newMagicRateLimiter()

// resetMagicRateLimiter : helper test.
func resetMagicRateLimiter() {
	globalMagicRateLimiter = newMagicRateLimiter()
}

// LoginMagicSubmit POST /login : reçoit email, génère token, envoie magic link.
// Rate-limit 3/15min/IP. Token random hex 32 bytes (64 chars).
func LoginMagicSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, deps.Config.TrustProxyHeaders)
		if !globalMagicRateLimiter.Allow(ip) {
			deps.Logger.Warn("login_magic_rate_limited", "ip_hash_prefix", hashIPShort(ip, deps.Config.CookieSecret))
			http.Error(w, "trop de demandes (3 / 15 minutes)", http.StatusTooManyRequests)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "form invalide", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		if email == "" || !strings.Contains(email, "@") {
			http.Error(w, "email invalide", http.StatusBadRequest)
			return
		}
		returnURL := r.FormValue("return_url")
		if returnURL == "" {
			returnURL = "/"
		}
		if !isSafeRedirectURL(returnURL, deps.Config.BaseURL) {
			returnURL = "/"
		}

		// Lookup user existant (NULL si first-time, créé au callback).
		var userID sql.NullString
		var existing string
		if err := deps.DB.QueryRowContext(r.Context(),
			`SELECT id FROM users WHERE LOWER(email) = ?`, email,
		).Scan(&existing); err == nil {
			userID = sql.NullString{String: existing, Valid: true}
		}

		ipHash := hashIPShort(ip, deps.Config.CookieSecret)
		idStore := &identity.Store{DB: deps.DB}
		var uidStr string
		if userID.Valid {
			uidStr = userID.String
		}
		issued, err := idStore.IssueMagicToken(r.Context(), email, uidStr, returnURL, ipHash, identity.LoginMagicTokenTTL)
		if err != nil {
			deps.Logger.Error("login_magic_insert", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		token := issued.Token

		// Envoyer email magic link via mailer outbox.
		callbackURL := deps.Config.BaseURL + "/login/callback?token=" + token
		if deps.Mailer != nil {
			subject := "Connexion à votre site associatif"
			bodyText := "Cliquez sur ce lien pour vous connecter (valable 15 minutes) :\n\n" + callbackURL +
				"\n\nSi vous n'avez pas demandé ce lien, ignorez ce message."
			bodyHTML := `<p>Cliquez <a href="` + callbackURL + `">ici</a> pour vous connecter (valable 15 minutes).</p>` +
				`<p><small>Si vous n'avez pas demandé ce lien, ignorez ce message.</small></p>`
			_ = deps.Mailer.Enqueue(r.Context(), email, subject, bodyText, bodyHTML)
		}

		deps.Logger.Info("login_magic_sent",
			"email_hash_prefix", emailHashShort(email),
			"existing_user", userID.Valid,
			"ip_hash_prefix", ipHash)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html><html lang="fr"><head><meta charset="utf-8"><title>Vérifiez votre email</title></head><body><main style="max-width:480px;margin:80px auto;padding:24px;font-family:system-ui,sans-serif"><h1>Vérifiez votre email</h1><p>Un lien de connexion vous a été envoyé. Il est valable 15 minutes.</p><p><small>Pensez à vérifier vos spams.</small></p></main></body></html>`)) //nolint:errcheck
	}
}

// LoginMagicCallback GET /login/callback?token=X : valide le token, set session.
func LoginMagicCallback(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" || len(token) != 64 {
			renderMagicError(w, "Lien invalide.")
			return
		}

		var email, returnURL string
		var userID sql.NullString
		var expiresAt string
		var usedAt sql.NullString
		err := deps.DB.QueryRowContext(r.Context(), `
			SELECT email, user_id, return_url, expires_at, used_at
			FROM login_magic_tokens WHERE token = ?
		`, token).Scan(&email, &userID, &returnURL, &expiresAt, &usedAt)
		if errors.Is(err, sql.ErrNoRows) {
			renderMagicError(w, "Lien inconnu ou expiré.")
			return
		}
		if err != nil {
			deps.Logger.Error("login_magic_lookup", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		if usedAt.Valid {
			renderMagicError(w, "Ce lien a déjà été utilisé. Demandez un nouveau lien.")
			return
		}
		exp, _ := time.Parse("2006-01-02 15:04:05", expiresAt)
		if time.Now().UTC().After(exp) {
			renderMagicError(w, "Ce lien a expiré (>15 minutes). Demandez un nouveau lien.")
			return
		}

		// Consommer le token de façon atomique AVANT toute autre op (anti-rejeu) :
		// l'UPDATE conditionnel `used_at IS NULL` + la vérification d'effet (une seule
		// ligne consommée) ferment la fenêtre TOCTOU entre la lecture usedAt ci-dessus
		// et l'établissement de session. Un échec d'écriture ou un second clic ne pose
		// jamais la session.
		consumeRes, err := deps.DB.ExecContext(r.Context(),
			`UPDATE login_magic_tokens SET used_at = CURRENT_TIMESTAMP WHERE token = ? AND used_at IS NULL`, token)
		if err != nil {
			deps.Logger.Error("login_magic_consume", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		if n, _ := consumeRes.RowsAffected(); n != 1 {
			renderMagicError(w, "Ce lien a déjà été utilisé. Demandez un nouveau lien.")
			return
		}

		var finalUserID string
		idStore := &identity.Store{DB: deps.DB}
		if userID.Valid {
			finalUserID = userID.String
		} else {
			u, err := idStore.CreateAccount(r.Context(), identity.CreateAccountOpts{
				Email: email, DisplayName: email, Active: true,
			})
			if errors.Is(err, identity.ErrEmailTaken) {
				finalUserID, err = idStore.GetUserIDByEmail(r.Context(), email)
			} else if err != nil {
				deps.Logger.Error("login_magic_user_create", "err", err.Error())
				renderMagicError(w, "Erreur création compte.")
				return
			} else {
				finalUserID = u.ID
			}
			if finalUserID == "" {
				renderMagicError(w, "Erreur création compte.")
				return
			}
		}

		// Set session cookie.
		middleware.SetSessionCookie(w, finalUserID, deps.Config.CookieSecret, deps.Config.CookieSecure())

		deps.Logger.Info("login_magic_consumed",
			"user_id", finalUserID, "email_hash_prefix", emailHashShort(email),
			"first_time", !userID.Valid)

		// Redirect return_url ou /.
		if returnURL == "" {
			returnURL = "/"
		}
		if !isSafeRedirectURL(returnURL, deps.Config.BaseURL) {
			returnURL = "/"
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
	}
}

// isSafeRedirectURL valide qu'une return_url est sûre contre open-redirect.
// Accepte un chemin relatif (/...) ou un host approuvé (BaseURL). Refuse les
// protocol-relative URLs (//evil.fr), les schémas absolus (https://evil.fr) et
// tout ce qui ne commence pas par /.
func isSafeRedirectURL(rawURL string, baseURL string) bool {
	if rawURL == "" || rawURL == "/" {
		return true
	}
	// Refuser les chemins absents de '/' initial (protocol-relative, data:, etc.)
	if !strings.HasPrefix(rawURL, "/") {
		return false
	}
	// Refuser les double-slash (//evil.fr → protocol-relative)
	if strings.HasPrefix(rawURL, "//") {
		return false
	}
	// Parser pour vérifier qu'il s'agit bien d'un chemin relatif, pas d'un URL absolu
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		return false
	}
	return true
}

// hashIPShort retourne un hash court pour audit logs sans leak IP brute.
func hashIPShort(ip string, secret []byte) string {
	h := sha256.New()
	h.Write([]byte(ip))
	h.Write(secret)
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// emailHashShort retourne un hash court d'email.
func emailHashShort(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:8])
}

// renderMagicError affiche une page erreur HTML simple.
func renderMagicError(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusBadRequest)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html><html lang="fr"><head><meta charset="utf-8"><title>Lien invalide</title></head><body><main style="max-width:480px;margin:80px auto;padding:24px;font-family:system-ui,sans-serif"><h1>Lien invalide</h1><p>` + msg + `</p><p><a href="/login">Demander un nouveau lien</a></p></main></body></html>`)) //nolint:errcheck
}

// Helpers context utilisés par tests.
var _ = context.Background
