// CLAUDE:SUMMARY Password reset full flow : GET/POST /forgot (request reset), GET/POST /reset (consume token + new password).
// CLAUDE:WARN Sécurité : (1) réponse identique pour email connu/inconnu (anti user-enum) ;
// (2) token 24-bytes urandom hex 48 chars, single-use, TTL 1h ; (3) RGPD : ip_hash SHA256+COOKIE_SECRET ;
// (4) après reset : invalidate toutes sessions cookies (best-effort : changement password_hash invalide les old sessions HMAC).
package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
	"golang.org/x/crypto/bcrypt"
)

// passwordResetTokenTTL : durée de validité d'un token. 1h = compromis sécurité/UX.
const passwordResetTokenTTL = 1 * time.Hour

// handleForgotForm : GET /forgot → form email.
func handleForgotForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		csrfToken := middleware.CSRFToken(r.Context())
		renderPage(w, r, deps, "Mot de passe oublié", pages.ForgotPasswordForm(csrfToken))
	}
}

// handleForgotSubmit : POST /forgot → enqueue mail si email connu, message identique sinon.
// Anti user-enum : la même réponse "Si un compte existe..." est rendue.
func handleForgotSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		if email == "" {
			middleware.PushFlash(w, "error", "Email obligatoire.")
			http.Redirect(w, r, "/forgot", http.StatusSeeOther)
			return
		}
		emailHash := middleware.HashEmail(email)
		deps.Logger.Info("password_reset_requested", "req_id", reqID, "email_hash", emailHash)

		// Lookup user, ne révèle pas l'existence dans la réponse.
		var userID string
		err := deps.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&userID)
		if err == nil && userID != "" {
			token, terr := createResetToken(ctx, deps.DB, userID, r.RemoteAddr, deps.Config.CookieSecret)
			if terr == nil && deps.Mailer != nil {
				resetURL := deps.Config.BaseURL + "/reset?token=" + token
				deps.Mailer.Enqueue(ctx, email, //nolint:errcheck
					"NONPOSSUMUS — réinitialisation du mot de passe",
					"Pour réinitialiser votre mot de passe, cliquez sur ce lien (valable 1h) : "+resetURL+
						"\n\nSi vous n'avez pas demandé cette réinitialisation, ignorez ce message.",
					`<p>Pour réinitialiser votre mot de passe, cliquez <a href="`+resetURL+`">ici</a> (valable 1h).</p>`+
						`<p style="color:#666;font-size:0.9em">Si vous n'avez pas demandé cette réinitialisation, ignorez ce message.</p>`,
				)
				deps.Logger.Info("password_reset_email_enqueued", "req_id", reqID, "user_id", userID, "email_hash", emailHash)
			} else if terr != nil {
				deps.Logger.Error("password_reset_token_create_failed", "req_id", reqID, "user_id", userID, "err", terr.Error())
			}
		} else {
			deps.Logger.Info("password_reset_unknown_email", "req_id", reqID, "email_hash", emailHash)
		}

		// Réponse identique dans tous les cas (anti user-enum).
		middleware.PushFlash(w, "info",
			"Si un compte existe pour cette adresse, un email de réinitialisation a été envoyé. "+
				"Vérifiez votre boîte (et les spams). Le lien est valable 1h.")
		http.Redirect(w, r, "/forgot", http.StatusSeeOther)
	}
}

// handleResetForm : GET /reset?token=X → form new password si token valide+non expiré+non consommé.
func handleResetForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			middleware.PushFlash(w, "error", "Lien invalide.")
			http.Redirect(w, r, "/forgot", http.StatusSeeOther)
			return
		}
		valid, _, err := lookupResetToken(r.Context(), deps.DB, token)
		if err != nil || !valid {
			middleware.PushFlash(w, "error", "Ce lien est invalide ou a expiré. Demandez un nouveau lien.")
			http.Redirect(w, r, "/forgot", http.StatusSeeOther)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPage(w, r, deps, "Nouveau mot de passe", pages.ResetPasswordForm(token, csrfToken))
	}
}

// handleResetSubmit : POST /reset → update bcrypt hash + invalidate token.
func handleResetSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		token := r.FormValue("token")
		password := r.FormValue("password")
		passwordConfirm := r.FormValue("password_confirm")

		valid, userID, err := lookupResetToken(ctx, deps.DB, token)
		if err != nil || !valid {
			middleware.PushFlash(w, "error", "Lien invalide ou expiré.")
			http.Redirect(w, r, "/forgot", http.StatusSeeOther)
			return
		}
		if len(password) < 12 {
			middleware.PushFlash(w, "error", "Mot de passe trop court (12 caractères minimum).")
			http.Redirect(w, r, "/reset?token="+token, http.StatusSeeOther)
			return
		}
		if password != passwordConfirm {
			middleware.PushFlash(w, "error", "Les deux mots de passe ne correspondent pas.")
			http.Redirect(w, r, "/reset?token="+token, http.StatusSeeOther)
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			deps.Logger.Error("password_reset_bcrypt", "req_id", reqID, "err", err.Error())
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}

		// Tx : update users.password_hash + mark token used.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback() //nolint:errcheck
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID); err != nil {
			deps.Logger.Error("password_reset_update_user", "req_id", reqID, "user_id", userID, "err", err.Error())
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE password_reset_tokens SET used_at = CURRENT_TIMESTAMP WHERE token = ?`, token); err != nil {
			deps.Logger.Error("password_reset_mark_used", "req_id", reqID, "token", safeTokenPrefix(token), "err", err.Error())
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			http.Error(w, "Erreur serveur", http.StatusInternalServerError)
			return
		}

		// Invalider la session courante (le user va re-login proprement).
		middleware.ClearSessionCookie(w)
		deps.Logger.Info("password_reset_completed", "req_id", reqID, "user_id", userID)
		middleware.PushFlash(w, "info", "Mot de passe mis à jour. Vous pouvez vous connecter.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// createResetToken génère un token urandom hex 48 chars + INSERT en DB.
func createResetToken(ctx context.Context, db *sql.DB, userID, remoteAddr string, cookieSecret []byte) (string, error) {
	tokenBytes := make([]byte, 24)
	rand.Read(tokenBytes) //nolint:errcheck
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().UTC().Add(passwordResetTokenTTL).Format("2006-01-02 15:04:05")

	clientIP, _, _ := net.SplitHostPort(remoteAddr)
	if clientIP == "" {
		clientIP = remoteAddr
	}
	h := sha256.New()
	h.Write([]byte(clientIP))
	h.Write(cookieSecret)
	ipHash := hex.EncodeToString(h.Sum(nil))

	_, err := db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens(token, user_id, expires_at, created_ip_hash) VALUES(?, ?, ?, ?)`,
		token, userID, expiresAt, ipHash)
	return token, err
}

// lookupResetToken : vérifie token existe + non expiré + non consommé.
// Retourne (valid, userID, err).
func lookupResetToken(ctx context.Context, db *sql.DB, token string) (bool, string, error) {
	if token == "" {
		return false, "", nil
	}
	var userID string
	var expiresAt string
	var usedAt sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT user_id, expires_at, used_at FROM password_reset_tokens WHERE token = ?`,
		token).Scan(&userID, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if usedAt.Valid {
		return false, "", nil
	}
	exp, perr := time.Parse("2006-01-02 15:04:05", expiresAt)
	if perr != nil {
		return false, "", perr
	}
	if time.Now().UTC().After(exp) {
		return false, "", nil
	}
	return true, userID, nil
}

// _staticUsedToShutUpVet évite l'erreur "imported and not used" si auth bcrypt
// transitivement ré-importé via go imports — placeholder neutre.
var _ = auth.Store{}
