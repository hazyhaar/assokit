// CLAUDE:SUMMARY Tests OAuth client — sandbox/prod URL switch + Vault.Use load (M-ASSOKIT-SPRINT3-S1).
package helloasso

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
)

const validKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func openOAuthDB(t *testing.T) (*sql.DB, *assets.Vault) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connectors (id TEXT PRIMARY KEY);
		INSERT INTO connectors(id) VALUES ('helloasso');
		CREATE TABLE connector_credentials (
			connector_id TEXT NOT NULL, key_name TEXT NOT NULL,
			encrypted_value BLOB NOT NULL,
			set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			set_by TEXT, rotated_at TEXT,
			PRIMARY KEY(connector_id, key_name)
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	v, err := assets.NewVault(db, validKey)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	return db, v
}

// TestSandboxVsProdURLSwitch : sandbox=true → URL helloasso-sandbox.com.
func TestSandboxVsProdURLSwitch(t *testing.T) {
	if !strings.Contains(TokenURLFor(true), "sandbox") {
		t.Errorf("TokenURLFor(true) = %q, attendu contient 'sandbox'", TokenURLFor(true))
	}
	if strings.Contains(TokenURLFor(false), "sandbox") {
		t.Errorf("TokenURLFor(false) = %q, attendu prod (pas sandbox)", TokenURLFor(false))
	}
	if !strings.Contains(APIBaseFor(true), "sandbox") {
		t.Errorf("APIBaseFor(true) = %q", APIBaseFor(true))
	}
}

// TestNewOAuthHTTPClient_LoadsSecretFromVault : Vault.Use appelé pour charger client_secret.
func TestNewOAuthHTTPClient_LoadsSecretFromVault(t *testing.T) {
	_, vault := openOAuthDB(t)
	if err := vault.Set(context.Background(), "helloasso", "client_secret", "VAULT_SECRET_42", "test"); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	// Mock OAuth token server qui retourne 200 OK avec un access_token bidon.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "test-token", "token_type": "Bearer", "expires_in": 1800,
		})
	}))
	defer srv.Close()

	cli, _, err := NewOAuthHTTPClient(context.Background(), vault, "helloasso", "client-id-123", srv.URL)
	if err != nil {
		t.Fatalf("NewOAuthHTTPClient: %v", err)
	}
	if cli == nil {
		t.Fatal("client nil")
	}

	// Faire un appel pour forcer le token fetch.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization header manquant ou wrong: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", apiSrv.URL+"/test", nil)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("api call: %v", err)
	}
	resp.Body.Close()
}

// TestNewOAuthHTTPClient_VaultMissingSecretFails : pas de credential en Vault → erreur Use.
func TestNewOAuthHTTPClient_VaultMissingSecretFails(t *testing.T) {
	_, vault := openOAuthDB(t)
	_, _, err := NewOAuthHTTPClient(context.Background(), vault, "helloasso", "id", "https://example.com/token")
	if err == nil {
		t.Error("attendu erreur (vault vide), got nil")
	}
}
