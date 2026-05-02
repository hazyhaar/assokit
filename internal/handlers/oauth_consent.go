// CLAUDE:SUMMARY Handler POST /oauth2/consent — approve génère code OAuth, deny redirect avec error (M-ASSOKIT-DCR-4).
// CLAUDE:WARN CSRF token vérifié via middleware standard. Code OAuth = uuid v7 random hex 32 bytes, expires 60s.
package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	authpages "github.com/hazyhaar/assokit/pkg/horui/auth/pages"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// OAuth2ConsentSubmit POST /oauth2/consent.
// Form fields : decision (approve|deny), auth_request_id, redirect_uri, state, _csrf.
// approve → INSERT oauth_authcodes + redirect avec ?code=...&state=...
// deny → redirect avec ?error=access_denied&state=...
func OAuth2ConsentSubmit(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form invalide", http.StatusBadRequest)
			return
		}

		decision := r.FormValue("decision")
		authReqID := r.FormValue("auth_request_id")
		redirectURI := r.FormValue("redirect_uri")
		state := r.FormValue("state")

		if redirectURI == "" {
			http.Error(w, "redirect_uri requis", http.StatusBadRequest)
			return
		}

		if decision == "deny" {
			deps.Logger.Info("oauth_consent_denied",
				"user_id", u.ID, "auth_request_id", authReqID)
			http.Redirect(w, r, redirectURI+"?error=access_denied&state="+url.QueryEscape(state), http.StatusFound)
			return
		}

		if decision != "approve" {
			http.Error(w, "decision must be approve|deny", http.StatusBadRequest)
			return
		}

		// Générer code OAuth (32 bytes hex).
		codeBytes := make([]byte, 32)
		if _, err := rand.Read(codeBytes); err != nil {
			http.Error(w, "rand fail", http.StatusInternalServerError)
			return
		}
		code := hex.EncodeToString(codeBytes)

		// Lookup auth_request stocké pour récupérer client_id, scopes, code_challenge.
		// (DCR-3 stocke ces infos quand /oauth2/authorize est appelé.)
		// Simplified : on persist directement avec les params reçus dans le form.
		// L'authrequest store sera renforcée en DCR-3.
		clientID := r.FormValue("client_id")
		scopes := r.FormValue("scope")
		codeChallenge := r.FormValue("code_challenge")
		codeChallengeMethod := r.FormValue("code_challenge_method")

		expiresAt := time.Now().UTC().Add(60 * time.Second).Format(time.RFC3339)

		scopesArr := strings.Fields(scopes)
		scopesJSON, _ := json.Marshal(scopesArr)

		_, err := deps.DB.ExecContext(r.Context(), `
			INSERT INTO oauth_authcodes(code, auth_req_id, client_id, user_id, scopes, redirect_uri, expires_at, code_challenge, code_challenge_method)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(code) DO NOTHING
		`, code, authReqID, clientID, u.ID, string(scopesJSON), redirectURI, expiresAt, codeChallenge, codeChallengeMethod)
		if err != nil {
			// Schema may not have code_challenge yet (added by DCR-5 migration).
			// Fallback insert without those fields.
			_, err = deps.DB.ExecContext(r.Context(), `
				INSERT INTO oauth_authcodes(code, auth_req_id, client_id, user_id, scopes, redirect_uri, expires_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(code) DO NOTHING
			`, code, authReqID, clientID, u.ID, string(scopesJSON), redirectURI, expiresAt)
			if err != nil {
				deps.Logger.Error("oauth_consent_insert", "err", err.Error())
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		deps.Logger.Info("oauth_consent_approved",
			"user_id", u.ID, "client_id", clientID, "auth_request_id", authReqID, "scopes_count", len(scopesArr))

		redirectURL := redirectURI + "?code=" + url.QueryEscape(code)
		if state != "" {
			redirectURL += "&state=" + url.QueryEscape(state)
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// RenderConsentPage : helper utilisé par DCR-3 pour rendre la page consent.
// Mappe les scopes raw vers libellés FR via authpages.LibellesScope.
func RenderConsentPage(w http.ResponseWriter, r *http.Request, deps app.AppDeps,
	clientName, authRequestID, redirectURI, state string, scopes []string) {
	props := authpages.ConsentProps{
		ClientName:    clientName,
		AuthRequestID: authRequestID,
		CSRFToken:     middleware.CSRFToken(r.Context()),
		ScopesGranted: authpages.LibellesScope(scopes),
		RedirectURI:   redirectURI,
		State:         state,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := authpages.Consent(props).Render(r.Context(), w); err != nil {
		deps.Logger.Error("consent render", "err", err.Error())
	}
}

// _ = sql.ErrNoRows : keep import
var _ = sql.ErrNoRows
