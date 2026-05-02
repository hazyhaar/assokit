// CLAUDE:SUMMARY DCR Register RFC 7591 — public/confidential client + persist (M-ASSOKIT-DCR-1).
// CLAUDE:WARN public client (token_endpoint_auth_method="none") → client_secret vide, PKCE obligatoire au /oauth2/token (impl S5).
// Anti-spam : caller (handler HTTP) doit appliquer rate-limit 5/h/IP avant Register.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RegisterRequest : RFC 7591 client registration request.
type RegisterRequest struct {
	ClientName        string   `json:"client_name"`
	RedirectURIs      []string `json:"redirect_uris"`
	GrantTypes        []string `json:"grant_types"`
	TokenEndpointAuth string   `json:"token_endpoint_auth_method"`
	Scope             string   `json:"scope"`
}

// RegisterResponse : RFC 7591 client registration response.
type RegisterResponse struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt      int64    `json:"client_id_issued_at"`
	ClientSecretExpiresAt int64    `json:"client_secret_expires_at"`
	ClientName            string   `json:"client_name,omitempty"`
	RedirectURIs          []string `json:"redirect_uris"`
	GrantTypes            []string `json:"grant_types"`
	TokenEndpointAuth     string   `json:"token_endpoint_auth_method"`
	Scope                 string   `json:"scope,omitempty"`
}

// Erreurs sentinelles.
var (
	ErrInvalidRedirectURI = errors.New("dcr: redirect_uri doit être https:// (sauf localhost http)")
	ErrMissingRedirectURI = errors.New("dcr: redirect_uris requis (au moins 1)")
	ErrInvalidGrantType   = errors.New("dcr: grant_type doit être authorization_code ou refresh_token")
	ErrInvalidAuthMethod  = errors.New("dcr: token_endpoint_auth_method invalide")
)

// validAuthMethods : RFC 7591 + extension client_secret_post.
var validAuthMethods = map[string]bool{
	"none":                true, // public client
	"client_secret_basic": true,
	"client_secret_post":  true,
}

// validGrantTypes : MVP scope.
var validGrantTypes = map[string]bool{
	"authorization_code": true,
	"refresh_token":      true,
}

// ValidateRedirectURI : https obligatoire sauf localhost (http://localhost ou http://127.0.0.1).
func ValidateRedirectURI(uri string) error {
	if uri == "" {
		return ErrInvalidRedirectURI
	}
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRedirectURI, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := strings.SplitN(u.Host, ":", 2)[0]
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return ErrInvalidRedirectURI
}

// Validate vérifie qu'une RegisterRequest est conforme RFC 7591 (defaults appliqués).
// Retourne erreur typée si invalide.
func (req *RegisterRequest) Validate() error {
	if len(req.RedirectURIs) == 0 {
		return ErrMissingRedirectURI
	}
	for _, u := range req.RedirectURIs {
		if err := ValidateRedirectURI(u); err != nil {
			return err
		}
	}
	if req.TokenEndpointAuth == "" {
		req.TokenEndpointAuth = "client_secret_basic" // RFC default
	}
	if !validAuthMethods[req.TokenEndpointAuth] {
		return fmt.Errorf("%w: %q", ErrInvalidAuthMethod, req.TokenEndpointAuth)
	}
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code"}
	}
	for _, gt := range req.GrantTypes {
		if !validGrantTypes[gt] {
			return fmt.Errorf("%w: %q", ErrInvalidGrantType, gt)
		}
	}
	return nil
}

// IsPublicClient retourne true si le client est public (PKCE obligatoire).
func (req *RegisterRequest) IsPublicClient() bool {
	return req.TokenEndpointAuth == "none"
}

// Register insère un nouveau client en DB. Génère client_id (UUID v7-style).
// Public client → client_secret vide. Confidential → secret 32 bytes hex.
func Register(ctx context.Context, db *sql.DB, req RegisterRequest) (*RegisterResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	clientID := uuid.New().String()
	resp := &RegisterResponse{
		ClientID:              clientID,
		ClientIDIssuedAt:      time.Now().UTC().Unix(),
		ClientSecretExpiresAt: 0,
		ClientName:            req.ClientName,
		RedirectURIs:          req.RedirectURIs,
		GrantTypes:            req.GrantTypes,
		TokenEndpointAuth:     req.TokenEndpointAuth,
		Scope:                 req.Scope,
	}

	var secretHash string
	if !req.IsPublicClient() {
		// Confidential : secret 32 bytes hex.
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return nil, fmt.Errorf("dcr: rand secret: %w", err)
		}
		secret := hex.EncodeToString(secretBytes)
		resp.ClientSecret = secret
		// Hash avec SHA256 (matching pattern existant validateBearerToken).
		sum := sha256.Sum256([]byte(secret))
		secretHash = hex.EncodeToString(sum[:])
	}

	redirectsJSON, _ := json.Marshal(req.RedirectURIs)
	grantsJSON, _ := json.Marshal(req.GrantTypes)
	scopesJSON := "[]"
	if req.Scope != "" {
		// scope = "a b c" (RFC 7591) → ["a","b","c"]
		scopesArr := strings.Fields(req.Scope)
		j, _ := json.Marshal(scopesArr)
		scopesJSON = string(j)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO oauth_clients(client_id, client_secret_hash, redirect_uris, grant_types, scopes, owner_user_id)
		VALUES (?, ?, ?, ?, ?, '')
	`, clientID, secretHash, string(redirectsJSON), string(grantsJSON), scopesJSON)
	if err != nil {
		return nil, fmt.Errorf("dcr: insert oauth_clients: %w", err)
	}

	return resp, nil
}
