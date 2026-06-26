package livekit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	_ "modernc.org/sqlite"
)

// masterKeyHex : clé AES-256 de test (64 chars hex). Jamais une vraie clé.
const masterKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// newConnectorTestDB monte un schéma minimal AVEC foreign_keys(1) — c'est
// précisément l'activation des pragmas FK qui exposait, en prod, les bugs
// d'ordre d'écriture (audit 2026-06-13). Le test les couvre donc en mémoire.
func newConnectorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY) STRICT`,
		`CREATE TABLE connectors (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
			config_json TEXT NOT NULL DEFAULT '{}',
			configured_at TEXT, configured_by TEXT REFERENCES users(id),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		) STRICT`,
		`CREATE TABLE connector_credentials (
			connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
			key_name TEXT NOT NULL, encrypted_value BLOB NOT NULL,
			set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			set_by TEXT REFERENCES users(id), rotated_at TEXT,
			PRIMARY KEY (connector_id, key_name)
		) STRICT`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// configure réplique l'ordre correct d'AdminConnectorConfigure : ligne parente
// connectors d'abord (FK), puis les secrets au Vault.
func configure(t *testing.T, db *sql.DB, v *assets.Vault) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO connectors(id, enabled, config_json, configured_at, configured_by)
		 VALUES ('livekit', 1, '{}', CURRENT_TIMESTAMP, NULL)`); err != nil {
		t.Fatalf("insert connectors: %v", err)
	}
	for k, val := range map[string]string{
		"server_url": "wss://example.test/livekit-ws",
		"api_key":    "APItest",
		"api_secret": "secret-de-test-au-moins-32-caracteres-x",
	} {
		if err := v.Set(ctx, "livekit", k, val, ""); err != nil {
			t.Fatalf("Vault.Set %s: %v", k, err)
		}
	}
}

// TestConfigureThenRoomAccess : le cycle complet — configurer le connecteur
// (connectors + 3 secrets chiffrés) puis forger un jeton via RoomAccess — passe
// avec les clés étrangères activées. C'est le scénario qui n'était couvert par
// AUCUN test et n'échouait qu'en prod (3 bugs : ordre FK, api_key/server_url mal
// routés en clair, set_by FK). Audit 2026-06-13.
func TestConfigureThenRoomAccess(t *testing.T) {
	db := newConnectorTestDB(t)
	v, err := assets.NewVault(db, masterKeyHex)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	configure(t, db, v)

	conn := New(v)
	token, serverURL, err := conn.RoomAccess(context.Background(), "salon-xyz", "user-1", time.Hour)
	if err != nil {
		t.Fatalf("RoomAccess: %v", err)
	}
	if serverURL != "wss://example.test/livekit-ws" {
		t.Errorf("serverURL = %q (les 3 paramètres doivent être lus au Vault, pas seul api_secret)", serverURL)
	}
	// Un JWT a 3 segments séparés par des points.
	if n := strings.Count(token, "."); n != 2 {
		t.Errorf("token = %q (n'est pas un JWT à 3 segments)", token)
	}
}

// TestRoomAccess_NonConfigured : sans secrets au Vault, RoomAccess échoue
// proprement (erreur typée), jamais un panic ni un token vide silencieux.
func TestRoomAccess_NonConfigured(t *testing.T) {
	db := newConnectorTestDB(t)
	v, _ := assets.NewVault(db, masterKeyHex)
	conn := New(v)
	if _, _, err := conn.RoomAccess(context.Background(), "salon-xyz", "user-1", time.Hour); err == nil {
		t.Fatal("RoomAccess sans config devrait échouer (credential absent)")
	}
}

// TestVaultSet_WrongOrder_FKViolation : documente la régression — écrire un
// secret AVANT la ligne parente connectors viole la FK (pragmas actifs). C'est
// exactement le bug d'origine d'AdminConnectorConfigure ; ce test fige l'ordre
// correct comme contrat.
func TestVaultSet_WrongOrder_FKViolation(t *testing.T) {
	db := newConnectorTestDB(t)
	v, _ := assets.NewVault(db, masterKeyHex)
	// Pas de ligne connectors préalable → la FK connector_credentials.connector_id casse.
	if err := v.Set(context.Background(), "livekit", "api_key", "APItest", ""); err == nil {
		t.Fatal("Vault.Set sans ligne connectors parente devrait violer la FK")
	}
}
