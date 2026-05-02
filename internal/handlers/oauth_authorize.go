// CLAUDE:SUMMARY GET /oauth2/authorize — session check + redirect /login si anonyme + PKCE mandatory pour public clients (M-ASSOKIT-DCR-3).
// CLAUDE:WARN Si client public (token_endpoint_auth_method=none) : code_challenge OBLIGATOIRE (PKCE), code_challenge_method=S256.
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// OAuth2AuthorizeHandler GET /oauth2/authorize.
// Params : response_type=code, client_id, redirect_uri, scope, state,
// code_challenge, code_challenge_method (S256 obligatoire pour public clients).
//
// Si pas de session NPS → redirect /login?return_url=<encoded full URL>.
// Si session → render page consent (DCR-4) avec scopes en FR.
func OAuth2AuthorizeHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		clientID := q.Get("client_id")
		redirectURI := q.Get("redirect_uri")
		responseType := q.Get("response_type")
		scope := q.Get("scope")
		state := q.Get("state")
		codeChallenge := q.Get("code_challenge")
		codeChallengeMethod := q.Get("code_challenge_method")

		if responseType != "code" {
			http.Error(w, "response_type doit être 'code'", http.StatusBadRequest)
			return
		}
		if clientID == "" {
			http.Error(w, "client_id requis", http.StatusBadRequest)
			return
		}
		if redirectURI == "" {
			http.Error(w, "redirect_uri requis", http.StatusBadRequest)
			return
		}

		// Lookup client + check redirect_uri whitelist + PKCE mandatory si public.
		client, err := lookupOAuthClient(r.Context(), deps.DB, clientID)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "client_id inconnu", http.StatusBadRequest)
			return
		}
		if err != nil {
			deps.Logger.Error("oauth_authorize_lookup", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		if !slicesContains(client.RedirectURIs, redirectURI) {
			http.Error(w, "redirect_uri non autorisée pour ce client", http.StatusBadRequest)
			return
		}

		// PKCE mandatory si client public (secret_hash vide = none auth method).
		if client.IsPublic() {
			if codeChallenge == "" {
				deps.Logger.Warn("oauth_authorize_pkce_missing", "client_id", clientID)
				http.Error(w, "code_challenge requis pour public client (PKCE obligatoire)", http.StatusBadRequest)
				return
			}
			if codeChallengeMethod != "S256" {
				http.Error(w, "code_challenge_method doit être 'S256'", http.StatusBadRequest)
				return
			}
		}

		// Si pas de session → redirect /login avec return_url = full authorize URL.
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			fullURL := r.URL.String()
			loginURL := "/login?return_url=" + url.QueryEscape(fullURL)
			http.Redirect(w, r, loginURL, http.StatusFound)
			return
		}

		// Session présente → render page consent.
		// Stocker auth_request en mémoire (ou DB) pour qu'au POST /oauth2/consent
		// on retrouve les params (notamment code_challenge). Le pattern stocke
		// les params en form hidden directement dans la page consent (no DB).
		clientName := clientID // TODO: lookup oauth_clients_metadata si dispo
		if client.Name != "" {
			clientName = client.Name
		}
		scopes := strings.Fields(scope)

		// Render via helper RenderConsentPage (livré DCR-4).
		// Note : DCR-4 attend des libellés FR ; on lui passe scopes raw, il map.
		RenderConsentPage(w, r, deps, clientName, "ar-"+state, redirectURI, state, scopes)
	}
}

// oauthClientRecord : sous-set des champs oauth_clients utiles pour authorize.
type oauthClientRecord struct {
	ID           string
	Name         string
	RedirectURIs []string
	SecretHash   string
}

// IsPublic : client public si client_secret_hash vide.
func (c *oauthClientRecord) IsPublic() bool {
	return c.SecretHash == ""
}

func lookupOAuthClient(ctx interface{ Done() <-chan struct{} }, db *sql.DB, clientID string) (*oauthClientRecord, error) {
	var c oauthClientRecord
	c.ID = clientID
	var redirectsJSON, secretHash string
	err := db.QueryRow(`SELECT redirect_uris, client_secret_hash FROM oauth_clients WHERE client_id = ?`, clientID).
		Scan(&redirectsJSON, &secretHash)
	if err != nil {
		return nil, err
	}
	c.SecretHash = secretHash
	if redirectsJSON != "" {
		_ = json.Unmarshal([]byte(redirectsJSON), &c.RedirectURIs)
	}
	return &c, nil
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
