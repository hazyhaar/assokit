// CLAUDE:SUMMARY Handlers OAuth consent + login social Google (M-ASSOKIT-OAUTH-1).
package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hazyhaar/assokit/internal/app"
	intoauth "github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// mountOAuthRoutes câble le provider OIDC et les routes consent + social login.
func mountOAuthRoutes(r chi.Router, deps app.AppDeps, oauthProvider http.Handler, store *intoauth.Storage) {
	r.Mount("/oauth2", oauthProvider)
	r.Get("/oauth2/consent", handleOAuthConsent(deps, store))
	r.Post("/oauth2/consent", handleOAuthConsentPost(deps, store))

	if os.Getenv("GOOGLE_CLIENT_ID") != "" {
		mountGoogleLogin(r, deps, store)
	}
}

// handleOAuthConsent affiche la page de consentement OAuth.
func handleOAuthConsent(deps app.AppDeps, store *intoauth.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "auth request id manquant", http.StatusBadRequest)
			return
		}
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri="+r.URL.RequestURI(), http.StatusFound)
			return
		}
		ar, err := store.AuthRequestByID(r.Context(), id)
		if err != nil {
			http.Error(w, "requête d'autorisation introuvable", http.StatusNotFound)
			return
		}
		effectivePerms := effectivePermissions(r.Context(), deps.DB, u.ID)
		renderConsentPage(w, id, ar.GetClientID(), ar.GetScopes(), effectivePerms)
	}
}

// handleOAuthConsentPost traite la décision de consentement.
func handleOAuthConsentPost(deps app.AppDeps, store *intoauth.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "non authentifié", http.StatusUnauthorized)
			return
		}
		r.ParseForm() //nolint:errcheck
		id := r.FormValue("id")
		decision := r.FormValue("decision")
		if id == "" {
			http.Error(w, "id manquant", http.StatusBadRequest)
			return
		}
		if decision == "deny" {
			http.Redirect(w, r, "/oauth2/authorize/callback?id="+id+"&error=access_denied", http.StatusFound)
			return
		}
		if err := store.CompleteAuthRequest(r.Context(), id, u.ID); err != nil {
			deps.Logger.Error("CompleteAuthRequest", slog.String("err", err.Error()))
			http.Error(w, "erreur interne", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/oauth2/authorize/callback?id="+id, http.StatusFound)
	}
}

func renderConsentPage(w http.ResponseWriter, authReqID, clientID string, scopes, effectivePerms []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	permList := ""
	for _, s := range scopes {
		granted := ""
		for _, p := range effectivePerms {
			if p == s {
				granted = " (accordé)"
				break
			}
		}
		permList += "<li>" + htmlEscape(s) + granted + "</li>"
	}
	html := `<!DOCTYPE html><html><head><title>Autorisation OAuth</title></head><body>
<h1>Demande d'autorisation</h1>
<p>L'application <strong>` + htmlEscape(clientID) + `</strong> demande accès aux permissions :</p>
<ul>` + permList + `</ul>
<form method="POST" action="/oauth2/consent">
  <input type="hidden" name="id" value="` + htmlEscape(authReqID) + `"/>
  <button type="submit" name="decision" value="allow">Autoriser</button>
  <button type="submit" name="decision" value="deny">Refuser</button>
</form></body></html>`
	w.Write([]byte(html)) //nolint:errcheck
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

func effectivePermissions(ctx context.Context, db *sql.DB, userID string) []string {
	rows, err := db.QueryContext(ctx,
		`SELECT p.name FROM user_effective_permissions uep
		 JOIN permissions p ON p.id = uep.permission_id
		 WHERE uep.user_id = ?`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		rows.Scan(&p) //nolint:errcheck
		perms = append(perms, p)
	}
	return perms
}

// ─── login social Google ───────────────────────────────────────────────────────

func mountGoogleLogin(r chi.Router, deps app.AppDeps, store *intoauth.Storage) {
	issuer := "https://accounts.google.com"
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	callbackURL := deps.Config.BaseURL + "/auth/google/callback"
	scopes := []string{oidc.ScopeOpenID, oidc.ScopeEmail, oidc.ScopeProfile}

	rpp, err := rp.NewRelyingPartyOIDC(context.Background(), issuer, clientID, clientSecret, callbackURL, scopes)
	if err != nil {
		deps.Logger.Warn("Google OIDC init failed", slog.String("err", err.Error()))
		return
	}

	r.Get("/auth/google/login", rp.AuthURLHandler(randomState, rpp))
	r.Get("/auth/google/callback", handleGoogleCallback(deps, store, rpp))
}

func randomState() string {
	tok, _ := intoauth.RandomToken()
	return tok
}

func handleGoogleCallback(deps app.AppDeps, store *intoauth.Storage, rpp rp.RelyingParty) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokens, err := rp.CodeExchange[*oidc.IDTokenClaims](r.Context(), r.URL.Query().Get("code"), rpp)
		if err != nil {
			http.Error(w, "échange code Google échoué: "+err.Error(), http.StatusBadRequest)
			return
		}
		email := tokens.IDTokenClaims.Email
		sub := tokens.IDTokenClaims.GetSubject()

		var userID string
		err = deps.DB.QueryRowContext(r.Context(),
			`SELECT user_id FROM oauth_external_links WHERE provider = 'google' AND external_id = ?`, sub).Scan(&userID)
		if err != nil {
			userID, err = findOrCreateSocialUser(r.Context(), deps, "google", sub, email)
			if err != nil {
				http.Error(w, "erreur création utilisateur: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		secure := strings.HasPrefix(deps.Config.BaseURL, "https://")
		middleware.SetSessionCookie(w, userID, deps.Config.CookieSecret, secure)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func findOrCreateSocialUser(ctx context.Context, deps app.AppDeps, provider, externalID, email string) (string, error) {
	var userID string
	err := deps.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, strings.ToLower(email)).Scan(&userID)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}

	if userID == "" {
		authStore := &auth.Store{DB: deps.DB}
		u, err := authStore.Register(ctx, email, "", email)
		if err != nil && err != auth.ErrEmailTaken {
			return "", err
		}
		if u != nil {
			userID = u.ID
		} else {
			// Race: email pris entre le SELECT et l'INSERT
			deps.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, strings.ToLower(email)).Scan(&userID) //nolint:errcheck
		}
		if userID != "" {
			assignDefaultGrade(ctx, deps.DB, userID)
		}
	}

	if userID != "" {
		deps.DB.ExecContext(ctx, //nolint:errcheck
			`INSERT OR IGNORE INTO oauth_external_links(id, user_id, provider, external_id, email) VALUES(?,?,?,?,?)`,
			uuid.NewString(), userID, provider, externalID, email)
	}

	return userID, nil
}

func assignDefaultGrade(ctx context.Context, db *sql.DB, userID string) {
	var gradeID string
	db.QueryRowContext(ctx, `SELECT id FROM grades WHERE name = 'member' AND system = 1`).Scan(&gradeID) //nolint:errcheck
	if gradeID != "" {
		rbacStore := &rbac.Store{DB: db}
		svc := &rbac.Service{Store: rbacStore, Cache: &rbac.Cache{}}
		svc.AssignGrade(ctx, userID, gradeID) //nolint:errcheck
	}
}
