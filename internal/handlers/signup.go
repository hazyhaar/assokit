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
	"github.com/google/uuid"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
	"golang.org/x/crypto/bcrypt"
)

// safeTokenPrefix retourne les 8 premiers chars du token pour les logs (jamais le token complet).
func safeTokenPrefix(token string) string {
	if len(token) < 8 {
		return token
	}
	return token[:8]
}

// Profils signup valides (extraits du HTML L'administrateur assokit.org).
var validProfils = map[string]bool{
	"adherent":   true,
	"lanceur":    true,
	"media":      true,
	"asso":       true,
	"expert":     true,
	"partenaire": true,
	"benevole":   true,
	"don":        true,
}

// handleSignupForm affiche le formulaire pour un profil donné.
func handleSignupForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profil := chi.URLParam(r, "profil")
		if !validProfils[profil] {
			http.NotFound(w, r)
			return
		}
		extras := signupExtraFields(profil)
		renderPage(w, r, deps,
			"Inscription — "+profilLabel(profil),
			pages.SignupForm(profil, profilLabel(profil), extras))
	}
}

// profilLabel : libellé humain pour un id profil.
func profilLabel(p string) string {
	switch p {
	case "adherent":
		return "Adhérent"
	case "lanceur":
		return "Lanceur d'alerte"
	case "media":
		return "Média / Journaliste"
	case "asso":
		return "Association"
	case "expert":
		return "Expert"
	case "partenaire":
		return "Partenaire"
	case "benevole":
		return "Bénévole"
	case "don":
		return "Donateur"
	default:
		return p
	}
}

// signupExtraFields : champs supplémentaires conditionnels par profil.
func signupExtraFields(p string) []pages.SignupExtraField {
	switch p {
	case "lanceur":
		return []pages.SignupExtraField{
			{Name: "secteur", Label: "Secteur concerné", Type: "text", Placeholder: "santé, finance, environnement…"},
			{Name: "urgence", Label: "Niveau d'urgence", Type: "text", Placeholder: "élevé / moyen / bas"},
		}
	case "media":
		return []pages.SignupExtraField{
			{Name: "nom_media", Label: "Nom du média", Type: "text", Required: true},
			{Name: "type_media", Label: "Type", Type: "text"},
		}
	case "asso":
		return []pages.SignupExtraField{
			{Name: "asso_nom", Label: "Nom de l'association", Type: "text", Required: true},
			{Name: "asso_role", Label: "Votre rôle", Type: "text"},
		}
	case "expert":
		return []pages.SignupExtraField{
			{Name: "specialite", Label: "Spécialité", Type: "text", Required: true},
			{Name: "barreau", Label: "Barreau / ordre / institution", Type: "text"},
		}
	case "partenaire":
		return []pages.SignupExtraField{
			{Name: "structure", Label: "Structure / organisation", Type: "text", Required: true},
		}
	case "don":
		return []pages.SignupExtraField{
			{Name: "telephone", Label: "Téléphone", Type: "tel"},
		}
	default:
		return nil
	}
}

// handleSignupSubmit traite la soumission du formulaire signup.
// Crée user + role member + magic link activation, enqueue 2 emails.
func handleSignupSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profil := chi.URLParam(r, "profil")
		if !validProfils[profil] {
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
		fieldsJSON := collectFields(r, profil)

		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)
		emailHash := middleware.HashEmail(email)

		deps.Logger.Info("signup_attempt",
			"req_id", reqID,
			"profile", profil,
			"email_hash", emailHash,
		)

		// TX atomique : signups + users + user_roles + activation_tokens
		token, err := createMember(ctx, deps.DB, email, displayName, profil, fieldsJSON, r.RemoteAddr, deps.Config.CookieSecret)
		if err != nil {
			stage := "create_member"
			if strings.Contains(err.Error(), "UNIQUE") {
				stage = "unique_violation"
				middleware.PushFlash(w, "error", "Cet email est déjà inscrit.")
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
				"Bienvenue sur NONPOSSUMUS — activez votre compte",
				"Cliquez sur ce lien pour activer votre compte : "+activationURL,
				"<p>Cliquez <a href=\""+activationURL+"\">ici</a> pour activer votre compte (valable 7 jours).</p>",
			)
			deps.Mailer.Enqueue(ctx, deps.Config.AdminEmail, //nolint:errcheck
				"[NPS] Nouvelle inscription : "+profil+" — "+email,
				fmt.Sprintf("Profil: %s\nEmail: %s\nNom: %s\nChamps: %s", profil, email, displayName, fieldsJSON),
				fmt.Sprintf("<b>Profil:</b> %s<br><b>Email:</b> %s<br><b>Nom:</b> %s", profil, email, displayName),
			)
		}

		http.Redirect(w, r, "/merci", http.StatusSeeOther)
	}
}

// createMember crée user + role member + activation token dans une TX atomique.
// Retourne le token d'activation ou une erreur.
func createMember(ctx context.Context, db *sql.DB, email, displayName, profil, fieldsJSON, remoteAddr string, cookieSecret []byte) (string, error) {
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
	signupID := uuid.New().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO signups(id, email, display_name, profile, fields_json, ip_hash) VALUES(?,?,?,?,?,?)`,
		signupID, email, displayName, profil, fieldsJSON, ipHash,
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

	userID := uuid.New().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)`,
		userID, email, string(hash), displayName,
	); err != nil {
		return "", fmt.Errorf("createMember user: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_roles(user_id, role_id) VALUES(?,?)`, userID, "member",
	); err != nil {
		return "", fmt.Errorf("createMember role: %w", err)
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

		middleware.SetSessionCookie(w, userID, deps.Config.CookieSecret, false)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// collectFields construit un JSON avec les champs conditionnels selon le profil.
func collectFields(r *http.Request, profil string) string {
	fields := map[string]string{}
	switch profil {
	case "adherent":
		fields["cotisation"] = r.FormValue("cotisation")
		fields["paiement"] = r.FormValue("paiement")
	case "lanceur":
		fields["pays"] = r.FormValue("pays")
		fields["secteur"] = r.FormValue("secteur")
		fields["urgence"] = r.FormValue("urgence")
	case "media":
		fields["nom_media"] = r.FormValue("nom_media")
		fields["type_media"] = r.FormValue("type_media")
		fields["url_media"] = r.FormValue("url_media")
	case "asso":
		fields["nom_asso"] = r.FormValue("nom_asso")
		fields["type_asso"] = r.FormValue("type_asso")
		fields["url_asso"] = r.FormValue("url_asso")
	case "expert":
		fields["specialite"] = r.FormValue("specialite")
		fields["barreau"] = r.FormValue("barreau")
	case "partenaire":
		fields["nom_partenaire"] = r.FormValue("nom_partenaire")
		fields["proposition"] = r.FormValue("proposition_partenaire")
	case "benevole":
		fields["dispo"] = r.FormValue("dispo_benevole")
		fields["domaine"] = r.FormValue("domaine_benevole")
	case "don":
		fields["montant"] = r.FormValue("montant_don")
		fields["paiement"] = r.FormValue("paiement_don")
	}
	fields["message"] = r.FormValue("message")
	fields["source"] = r.FormValue("source")
	b, _ := json.Marshal(fields)
	return string(b)
}
