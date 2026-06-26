// CLAUDE:SUMMARY Handler signup 8 profils — crée user+role member+magic link, enqueue emails.
package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/eventsink"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
	"github.com/hazyhaar/assokit/pkg/uid"
	"golang.org/x/crypto/bcrypt"
)

// siteName retourne le nom de l'instance (branding courant), fallback "Assokit".
// Utilisé dans les sujets d'emails — le core reste générique, le nom vient du branding.
func siteName() string {
	if n := theme.Brand().Name; n != "" {
		return n
	}
	return "Assokit"
}

// safeTokenPrefix retourne les 8 premiers chars du token pour les logs (jamais le token complet).
func safeTokenPrefix(token string) string {
	if len(token) < 8 {
		return token
	}
	return token[:8]
}

// handleSignupForm affiche le formulaire pour un profil donné.
func handleSignupForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profilID := chi.URLParam(r, "profil")
		profile, ok := signupprofile.Find(deps.Profils, profilID)
		if !ok {
			http.NotFound(w, r)
			return
		}
		extras := signupExtraFields(profile)
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps,
			"Inscription — "+profile.Label,
			views.SignupForm(profile.ID, profile.Label, csrfToken, extras))
	}
}

// signupExtraFields traduit les champs extra du catalogue vers la vue du formulaire.
func signupExtraFields(p signupprofile.Profile) []views.SignupExtraField {
	if len(p.Extra) == 0 {
		return nil
	}
	out := make([]views.SignupExtraField, 0, len(p.Extra))
	for _, f := range p.Extra {
		out = append(out, views.SignupExtraField{
			Name:     f.Name,
			Label:    f.Label,
			Type:     f.Type,
			Required: f.Required,
		})
	}
	return out
}

// handleSignupSubmit traite la soumission du formulaire signup.
// Crée user + role member + magic link activation, enqueue 2 emails.
func handleSignupSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profil := chi.URLParam(r, "profil")
		profile, ok := signupprofile.Find(deps.Profils, profil)
		if !ok {
			http.NotFound(w, r)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "formulaire invalide", http.StatusBadRequest)
			return
		}

		email := strings.TrimSpace(r.FormValue("email"))
		prenom := strings.TrimSpace(r.FormValue("prenom"))
		nom := strings.TrimSpace(r.FormValue("nom"))
		if email == "" || prenom == "" {
			middleware.PushFlash(w, "error", "Email et prénom obligatoires.")
			http.Redirect(w, r, "/adherer/"+profil, http.StatusSeeOther)
			return
		}
		displayName := prenom
		if nom != "" {
			displayName = prenom + " " + nom
		}

		// Collecte champs conditionnels selon profil
		fieldsJSON := collectFields(r, profile)

		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)
		emailHash := middleware.HashEmail(email)

		deps.Logger.Info("signup_attempt",
			"req_id", reqID,
			"profile", profil,
			"email_hash", emailHash,
		)

		// TX atomique : signups + users + user_roles + activation_tokens
		token, err := createMember(ctx, deps.DB, email, displayName, profile, fieldsJSON, r.RemoteAddr, deps.Config.CookieSecret)
		if err != nil {
			stage := "create_member"
			if strings.Contains(err.Error(), "UNIQUE") {
				stage = "unique_violation"
				// Email déjà inscrit : analyser l'état (compte actif vs en attente d'activation)
				// pour proposer une UX adaptée plutôt qu'un message dead-end.
				existingToken, alreadyActivated, lookupErr := lookupExistingSignup(ctx, deps.DB, email)
				switch {
				case lookupErr != nil:
					middleware.PushFlash(w, "error", "Cet email est déjà inscrit. Si vous n'avez pas reçu l'email d'activation, contactez-nous.")
				case alreadyActivated:
					middleware.PushFlash(w, "info", "Cet email est déjà inscrit et activé. Vous pouvez vous connecter.")
					deps.Logger.Info("signup_unique_already_active", "req_id", reqID, "email_hash", emailHash)
					http.Redirect(w, r, "/login", http.StatusSeeOther)
					return
				default:
					// Compte non activé : ré-enqueue un mail d'activation avec le token existant.
					if deps.Mailer != nil && existingToken != "" {
						activationURL := deps.Config.BaseURL + "/activate/" + existingToken
						deps.Mailer.Enqueue(ctx, email, //nolint:errcheck
							siteName()+" — votre lien d'activation (renvoi)",
							"Cliquez sur ce lien pour activer votre compte : "+activationURL,
							"<p>Cliquez <a href=\""+activationURL+"\">ici</a> pour activer votre compte (valable 7 jours).</p>",
						)
						deps.Logger.Info("signup_resend_activation", "req_id", reqID, "email_hash", emailHash)
						middleware.PushFlash(w, "info", "Vous êtes déjà pré-inscrit. Un nouveau mail d'activation vient de vous être envoyé. Vérifiez aussi vos spams.")
					} else {
						middleware.PushFlash(w, "error", "Cet email est déjà inscrit mais le compte n'est pas activé. Contactez-nous.")
					}
				}
			} else {
				middleware.PushFlash(w, "error", "Erreur lors de l'inscription, réessayez.")
			}
			deps.Logger.Error("signup_failed",
				"req_id", reqID,
				"profile", profil,
				"email_hash", emailHash,
				"stage", stage,
				"err", err.Error(),
			)
			http.Redirect(w, r, "/adherer/"+profil, http.StatusSeeOther)
			return
		}

		deps.Logger.Info("signup_created",
			"req_id", reqID,
			"profile", profil,
			"email_hash", emailHash,
			"token_prefix", safeTokenPrefix(token),
		)

		// Enqueue emails si mailer disponible
		if deps.Mailer != nil {
			activationURL := deps.Config.BaseURL + "/activate/" + token
			// Emails best-effort : l'outbox garantit la livraison, l'erreur d'Enqueue ne doit pas bloquer la réponse HTTP.
			deps.Mailer.Enqueue(ctx, email, //nolint:errcheck
				"Bienvenue sur "+siteName()+" — activez votre compte",
				"Cliquez sur ce lien pour activer votre compte : "+activationURL,
				"<p>Cliquez <a href=\""+activationURL+"\">ici</a> pour activer votre compte (valable 7 jours).</p>",
			)
			deps.Mailer.Enqueue(ctx, deps.Config.AdminEmail, //nolint:errcheck
				"["+siteName()+"] Nouvelle inscription : "+profil+" — "+email,
				fmt.Sprintf("Profil: %s\nEmail: %s\nNom: %s\nChamps: %s", profil, email, displayName, fieldsJSON),
				fmt.Sprintf("<b>Profil:</b> %s<br><b>Email:</b> %s<br><b>Nom:</b> %s", profil, email, displayName),
			)
		}

		// Émet member.signup vers le Sink (webhook générique / bus pôle). Best-effort.
		if deps.EventSink != nil {
			if err := deps.EventSink.Emit(ctx, eventsink.Event{
				Type: "member.signup",
				Payload: map[string]any{
					"profile":      profil,
					"email":        email,
					"display_name": displayName,
				},
			}); err != nil {
				deps.Logger.Warn("signup_event_emit_failed", "req_id", reqID, "err", err.Error())
			}
		}

		http.Redirect(w, r, "/merci", http.StatusSeeOther)
	}
}

// lookupExistingSignup retourne le token d'activation le plus récent pour un email
// existant et un flag indiquant si le compte est déjà activé. Utilisé quand un POST
// /adherer/{profil} hit une UNIQUE violation pour offrir une UX adaptée (renvoi mail
// vs redirection login) plutôt qu'un dead-end "déjà inscrit".
func lookupExistingSignup(ctx context.Context, db *sql.DB, email string) (token string, activated bool, err error) {
	row := db.QueryRowContext(ctx, `
		SELECT t.token, t.used_at
		FROM activation_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE u.email = ?
		ORDER BY t.expires_at DESC
		LIMIT 1
	`, email)
	var tok string
	var usedAt sql.NullString
	if err := row.Scan(&tok, &usedAt); err != nil {
		return "", false, err
	}
	return tok, usedAt.Valid, nil
}

// createMember crée user + role member + activation token dans une TX atomique.
// Retourne le token d'activation ou une erreur.
func createMember(ctx context.Context, db *sql.DB, email, displayName string, profile signupprofile.Profile, fieldsJSON, remoteAddr string, cookieSecret []byte) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("createMember begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — no-op si Commit a réussi, erreur ignorée intentionnellement

	// ip_hash = SHA256(IP + COOKIE_SECRET) — jamais l'IP brute (RGPD)
	clientIP, _, _ := net.SplitHostPort(remoteAddr)
	if clientIP == "" {
		clientIP = remoteAddr
	}
	h := sha256.New()
	h.Write([]byte(clientIP))
	h.Write(cookieSecret)
	ipHash := hex.EncodeToString(h.Sum(nil))

	// INSERT signup
	signupID := uid.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO signups(id, email, display_name, profile, fields_json, ip_hash) VALUES(?,?,?,?,?,?)`,
		signupID, email, displayName, profile.ID, fieldsJSON, ipHash,
	); err != nil {
		return "", fmt.Errorf("createMember signup: %w", err)
	}

	// Password random (non communiqué — accès via magic link uniquement)
	pwRaw := make([]byte, 16)
	rand.Read(pwRaw) //nolint:errcheck — crypto/rand.Read ne retourne jamais d'erreur depuis Go 1.20
	hash, err := bcrypt.GenerateFromPassword(pwRaw, 12)
	if err != nil {
		return "", fmt.Errorf("createMember bcrypt: %w", err)
	}

	userID := uid.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
		userID, email, string(hash), displayName,
	); err != nil {
		return "", fmt.Errorf("createMember user: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_grades(user_id, grade_id) VALUES(?,?) ON CONFLICT DO NOTHING`, userID, profile.Grade(),
	); err != nil {
		return "", fmt.Errorf("createMember grade: %w", err)
	}

	// Magic link token valable 7 jours
	tokenBytes := make([]byte, 24)
	rand.Read(tokenBytes) //nolint:errcheck — crypto/rand.Read ne retourne jamais d'erreur depuis Go 1.20
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO activation_tokens(token, user_id, expires_at) VALUES(?,?,?)`,
		token, userID, expiresAt,
	); err != nil {
		return "", fmt.Errorf("createMember token: %w", err)
	}

	return token, tx.Commit()
}

// handleActivate consomme le magic link et pose un cookie de session.
func handleActivate(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)

		var userID string
		var expiresAt string
		var usedAt sql.NullString
		err := deps.DB.QueryRowContext(ctx,
			`SELECT user_id, expires_at, used_at FROM activation_tokens WHERE token=?`, token,
		).Scan(&userID, &expiresAt, &usedAt)

		if err != nil || usedAt.Valid {
			deps.Logger.Warn("signup_activate_invalid_token",
				"req_id", reqID,
				"token_prefix", safeTokenPrefix(token),
				"used", usedAt.Valid,
			)
			http.Error(w, "Lien invalide ou déjà utilisé.", http.StatusBadRequest)
			return
		}
		exp, _ := time.Parse("2006-01-02 15:04:05", expiresAt)
		if time.Now().After(exp) {
			deps.Logger.Warn("signup_activate_expired",
				"req_id", reqID,
				"user_id", userID,
				"token_prefix", safeTokenPrefix(token),
			)
			http.Error(w, "Lien expiré. Contactez contact@assokit.org.", http.StatusGone)
			return
		}

		deps.DB.ExecContext(ctx, `UPDATE activation_tokens SET used_at=CURRENT_TIMESTAMP WHERE token=?`, token) //nolint:errcheck — best-effort : l'utilisateur est connecté même si la mise à jour échoue

		deps.Logger.Info("signup_activated",
			"req_id", reqID,
			"user_id", userID,
		)

		middleware.SetSessionCookie(w, userID, deps.Config.CookieSecret, deps.Config.CookieSecure())
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// collectFields construit un JSON avec les champs extra du profil (catalogue) et
// les champs communs à tous les profils. Générique : aucune liste de champs codée
// en dur par profil — le catalogue injecté à la bordure définit les extras.
func collectFields(r *http.Request, profile signupprofile.Profile) string {
	fields := map[string]string{}
	for _, f := range profile.Extra {
		fields[f.Name] = r.FormValue(f.Name)
	}
	fields["message"] = r.FormValue("message")
	fields["source"] = r.FormValue("source")
	b, _ := json.Marshal(fields)
	return string(b)
}
