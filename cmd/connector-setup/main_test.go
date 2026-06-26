package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	_ "modernc.org/sqlite"
)

// masterKeyHex : clé AES-256 de test (64 chars hex). Jamais une vraie clé.
const masterKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	schema := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY) STRICT`,
		`CREATE TABLE connectors (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
			config_json TEXT NOT NULL DEFAULT '{}',
			configured_at TEXT,
			configured_by TEXT REFERENCES users(id),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		) STRICT`,
		`CREATE TABLE connector_credentials (
			connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
			key_name TEXT NOT NULL,
			encrypted_value BLOB NOT NULL,
			set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			set_by TEXT REFERENCES users(id),
			rotated_at TEXT,
			PRIMARY KEY (connector_id, key_name)
		) STRICT`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// TestApplyConnectorConfig vérifie l'écriture réelle : ligne connectors enabled=1,
// secrets chiffrés récupérables en clair via le Vault (round-trip), FK users
// respectée (set_by/configured_by NULL), ordre des clés déterministe.
func TestApplyConnectorConfig(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	secrets := map[string]string{
		"server_url": "wss://example.test/livekit-ws",
		"api_key":    "APItest",
		"api_secret": "s3cr3t-au-moins-quelque-chose",
	}

	keys, err := applyConnectorConfig(ctx, db, masterKeyHex, "livekit", secrets)
	if err != nil {
		t.Fatalf("applyConnectorConfig: %v", err)
	}
	if got := keys; len(got) != 3 || got[0] != "api_key" || got[1] != "api_secret" || got[2] != "server_url" {
		t.Fatalf("clés = %v, attendu tri [api_key api_secret server_url]", got)
	}

	// connectors enabled=1.
	var enabled int
	if err := db.QueryRow(`SELECT enabled FROM connectors WHERE id='livekit'`).Scan(&enabled); err != nil {
		t.Fatalf("select connectors: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d, attendu 1", enabled)
	}

	// Round-trip Vault : chaque secret déchiffré doit valoir l'original.
	v, err := assets.NewVault(db, masterKeyHex)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	for k, want := range secrets {
		var got string
		if err := v.Use(ctx, "livekit", k, func(pt string) error { got = pt; return nil }); err != nil {
			t.Fatalf("Vault.Use %s: %v", k, err)
		}
		if got != want {
			t.Errorf("secret %s = %q, attendu %q", k, got, want)
		}
	}
}

// TestApplyConnectorConfig_Idempotent : rejouer ne duplique pas et met à jour.
func TestApplyConnectorConfig_Idempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	s1 := map[string]string{"api_key": "first"}
	if _, err := applyConnectorConfig(ctx, db, masterKeyHex, "livekit", s1); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	s2 := map[string]string{"api_key": "second"}
	if _, err := applyConnectorConfig(ctx, db, masterKeyHex, "livekit", s2); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	// Une seule ligne connectors, une seule credential, valeur mise à jour.
	var nConn, nCred int
	db.QueryRow(`SELECT count(*) FROM connectors WHERE id='livekit'`).Scan(&nConn)
	db.QueryRow(`SELECT count(*) FROM connector_credentials WHERE connector_id='livekit'`).Scan(&nCred)
	if nConn != 1 || nCred != 1 {
		t.Fatalf("après rejeu : connectors=%d creds=%d, attendu 1 et 1", nConn, nCred)
	}
	v, _ := assets.NewVault(db, masterKeyHex)
	var got string
	v.Use(ctx, "livekit", "api_key", func(pt string) error { got = pt; return nil })
	if got != "second" {
		t.Errorf("api_key = %q, attendu 'second' (mise à jour)", got)
	}
}

// TestCollectSecrets vérifie l'extraction des variables SECRET_*.
func TestCollectSecrets(t *testing.T) {
	t.Setenv("SECRET_api_key", "abc")
	t.Setenv("SECRET_server_url", "wss://x")
	t.Setenv("NOT_A_SECRET", "ignore")
	t.Setenv("SECRET_", "ignore-empty-name")
	got := collectSecrets()
	if got["api_key"] != "abc" || got["server_url"] != "wss://x" {
		t.Fatalf("collectSecrets = %v", got)
	}
	if _, ok := got["NOT_A_SECRET"]; ok {
		t.Error("NOT_A_SECRET ne doit pas être collecté")
	}
}
