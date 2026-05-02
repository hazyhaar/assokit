// CLAUDE:SUMMARY Magic link login zéro-password (M-ASSOKIT-DCR-2).
// CLAUDE:WARN Token expiry 15min, single-use via used_at. Rate-limit 3/15min/IP. ip_hash stocké, pas IP brute (RGPD).
package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// _ = auth pour éviter import unused si certains chemins removed.
var _ = "magic_link" // sentinel pour clarté package

const (
	magicTokenTTL    = 15 * time.Minute
	magicRateLimit   = 3
	magicRateWindow  = 15 * time.Minute
)

// magicRateLimiter : 3 demandes/15min/IP. Pattern identique à dcrRateLimiter.
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
		ip := clientIPFromRequest(r)
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

		// Lookup user existant (NULL si first-time, créé au callback).
		var userID sql.NullString
		var existing string
		if err := deps.DB.QueryRowContext(r.Context(),
			`SELECT id FROM users WHERE LOWER(email) = ?`, email,
		).Scan(&existing); err == nil {
			userID = sql.NullString{String: existing, Valid: true}
		}

		// Token random 32 bytes hex.
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			http.Error(w, "rand fail", http.StatusInternalServerError)
			return
		}
		token := hex.EncodeToString(tokenBytes)
		expiresAt := time.Now().UTC().Add(magicTokenTTL).Format("2006-01-02 15:04:05")
		ipHash := hashIPShort(ip, deps.Config.CookieSecret)

		_, err := deps.DB.ExecContext(r.Context(), `
			INSERT INTO login_magic_tokens(token, email, user_id, return_url, expires_at, ip_hash)
			VALUES (?, ?, ?, ?, ?, ?)
		`, token, email, userID, returnURL, expiresAt, ipHash)
		if err != nil {
			deps.Logger.Error("login_magic_insert", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

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

		// Marquer used_at AVANT toute autre op (anti-replay).
		_, _ = deps.DB.ExecContext(r.Context(),
			`UPDATE login_magic_tokens SET used_at = CURRENT_TIMESTAMP WHERE token = ?`, token)

		// Si first-time : créer user.
		var finalUserID string
		if userID.Valid {
			finalUserID = userID.String
		} else {
			finalUserID = uuid.New().String()
			_, err := deps.DB.ExecContext(r.Context(), `
				INSERT INTO users(id, email, password_hash, display_name)
				VALUES (?, ?, '', ?)
				ON CONFLICT(email) DO NOTHING
			`, finalUserID, email, email)
			if err != nil {
				deps.Logger.Error("login_magic_user_create", "err", err.Error())
				renderMagicError(w, "Erreur création compte.")
				return
			}
			// Si conflict (race avec autre token concurrent), récupérer l'id existant.
			_ = deps.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE email = ?`, email).Scan(&finalUserID)
		}

		// Set session cookie.
		secure := strings.HasPrefix(deps.Config.BaseURL, "https://")
		middleware.SetSessionCookie(w, finalUserID, deps.Config.CookieSecret, secure)

		deps.Logger.Info("login_magic_consumed",
			"user_id", finalUserID, "email_hash_prefix", emailHashShort(email),
			"first_time", !userID.Valid)

		// Redirect return_url ou /.
		if returnURL == "" {
			returnURL = "/"
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
	}
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
