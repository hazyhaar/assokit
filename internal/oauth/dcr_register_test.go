// CLAUDE:SUMMARY Tests gardiens DCR Register — public/confidential, validation, persist (M-ASSOKIT-DCR-1).
package oauth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const oauthClientsSchema = `
CREATE TABLE oauth_clients (
	client_id TEXT PRIMARY KEY,
	client_secret_hash TEXT NOT NULL DEFAULT '',
	redirect_uris TEXT NOT NULL DEFAULT '[]',
	grant_types TEXT NOT NULL DEFAULT '[]',
	scopes TEXT NOT NULL DEFAULT '[]',
	owner_user_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func openOAuthClientsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(oauthClientsSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestDCRRegister_PublicClientReturnsClientID : auth_method=none → secret vide.
func TestDCRRegister_PublicClientReturnsClientID(t *testing.T) {
	db := openOAuthClientsDB(t)
	resp, err := Register(context.Background(), db, RegisterRequest{
		ClientName:        "Claude Web",
		RedirectURIs:      []string{"https://claude.ai/oauth/callback"},
		GrantTypes:        []string{"authorization_code"},
		TokenEndpointAuth: "none",
		Scope:             "feedback.create",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.ClientID == "" {
		t.Error("client_id vide")
	}
	if resp.ClientSecret != "" {
		t.Errorf("public client a un secret : %q", resp.ClientSecret)
	}
	if resp.TokenEndpointAuth != "none" {
		t.Errorf("auth_method = %q", resp.TokenEndpointAuth)
	}
}

// TestDCRRegister_ConfidentialClientReturnsSecret : auth_method=client_secret_basic → secret 64 chars hex.
func TestDCRRegister_ConfidentialClientReturnsSecret(t *testing.T) {
	db := openOAuthClientsDB(t)
	resp, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"https://app.example.com/cb"},
		GrantTypes:        []string{"authorization_code"},
		TokenEndpointAuth: "client_secret_basic",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(resp.ClientSecret) != 64 {
		t.Errorf("secret len = %d, attendu 64 (32 bytes hex)", len(resp.ClientSecret))
	}
}

// TestDCRRegister_PersistsInDB : SELECT row insérée.
func TestDCRRegister_PersistsInDB(t *testing.T) {
	db := openOAuthClientsDB(t)
	resp, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"https://x.example.org/cb"},
		TokenEndpointAuth: "none",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var clientID, redirects, secretHash string
	err = db.QueryRow(`SELECT client_id, redirect_uris, client_secret_hash FROM oauth_clients WHERE client_id = ?`, resp.ClientID).Scan(&clientID, &redirects, &secretHash)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !strings.Contains(redirects, "x.example.org") {
		t.Errorf("redirect_uris = %q", redirects)
	}
	if secretHash != "" {
		t.Errorf("public client secretHash non vide : %q", secretHash)
	}
}

// TestDCRRegister_InvalidRedirectURIRejected : http (non localhost) refusé.
func TestDCRRegister_InvalidRedirectURIRejected(t *testing.T) {
	db := openOAuthClientsDB(t)
	_, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"http://malicious.example.com/cb"},
		TokenEndpointAuth: "none",
	})
	if !errors.Is(err, ErrInvalidRedirectURI) {
		t.Errorf("err = %v, attendu ErrInvalidRedirectURI", err)
	}
}

// TestDCRRegister_LocalhostHTTPAllowed : http://localhost OK pour dev.
func TestDCRRegister_LocalhostHTTPAllowed(t *testing.T) {
	db := openOAuthClientsDB(t)
	_, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"http://localhost:3000/cb", "http://127.0.0.1:8080/cb"},
		TokenEndpointAuth: "none",
	})
	if err != nil {
		t.Errorf("localhost http refusé : %v", err)
	}
}

// TestDCRRegister_MissingRedirectURIs : aucune URI → erreur.
func TestDCRRegister_MissingRedirectURIs(t *testing.T) {
	db := openOAuthClientsDB(t)
	_, err := Register(context.Background(), db, RegisterRequest{TokenEndpointAuth: "none"})
	if !errors.Is(err, ErrMissingRedirectURI) {
		t.Errorf("err = %v, attendu ErrMissingRedirectURI", err)
	}
}

// TestDCRRegister_InvalidAuthMethod : random method → erreur.
func TestDCRRegister_InvalidAuthMethod(t *testing.T) {
	db := openOAuthClientsDB(t)
	_, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"https://x/cb"},
		TokenEndpointAuth: "magic_jwt_assertion",
	})
	if !errors.Is(err, ErrInvalidAuthMethod) {
		t.Errorf("err = %v, attendu ErrInvalidAuthMethod", err)
	}
}

// TestDCRRegister_InvalidGrantType : grant_type unknown → erreur.
func TestDCRRegister_InvalidGrantType(t *testing.T) {
	db := openOAuthClientsDB(t)
	_, err := Register(context.Background(), db, RegisterRequest{
		RedirectURIs:      []string{"https://x/cb"},
		GrantTypes:        []string{"client_credentials"},
		TokenEndpointAuth: "none",
	})
	if !errors.Is(err, ErrInvalidGrantType) {
		t.Errorf("err = %v, attendu ErrInvalidGrantType", err)
	}
}
