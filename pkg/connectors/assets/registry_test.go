// CLAUDE:SUMMARY Tests gardiens Vault — Set/Use/Rotate/List + AES-GCM + plaintext zero-out (M-ASSOKIT-SPRINT2-S2).
package assets

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openVaultDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connectors (id TEXT PRIMARY KEY);
		INSERT INTO connectors(id) VALUES ('helloasso'), ('stripe');
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
	return db
}

// TestVault_SetEncryptsWithAESGCM : Set("test"), encrypted_value en DB != plaintext.
func TestVault_SetEncryptsWithAESGCM(t *testing.T) {
	db := openVaultDB(t)
	v, err := NewVault(db, validHexKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}

	if err := v.Set(context.Background(), "helloasso", "client_secret", "MY_SECRET", "admin-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var enc []byte
	db.QueryRow(`SELECT encrypted_value FROM connector_credentials WHERE connector_id='helloasso'`).Scan(&enc)
	if bytes.Contains(enc, []byte("MY_SECRET")) {
		t.Errorf("encrypted_value contient le plaintext en clair (longueur %d)", len(enc))
	}
	if len(enc) < 12+8 { // nonce + tag minimum
		t.Errorf("encrypted_value trop court : %d bytes", len(enc))
	}
}

// TestVault_UseCallbackReceivesPlaintext : Set + Use → callback reçoit "MY_SECRET".
func TestVault_UseCallbackReceivesPlaintext(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)
	v.Set(context.Background(), "helloasso", "client_secret", "MY_SECRET", "admin") //nolint:errcheck

	var got string
	err := v.Use(context.Background(), "helloasso", "client_secret", func(plaintext string) error {
		got = plaintext
		return nil
	})
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if got != "MY_SECRET" {
		t.Errorf("plaintext reçu = %q, attendu MY_SECRET", got)
	}
}

// TestVault_UseAbsentCredentialReturnsError : Use sur clé non settée → erreur.
func TestVault_UseAbsentCredentialReturnsError(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)
	err := v.Use(context.Background(), "helloasso", "missing", func(_ string) error { return nil })
	if err == nil {
		t.Error("Use sur clé absente devrait retourner erreur")
	}
}

// TestVault_RotateReplacesEncryptedValue : Set v1, Rotate v2 → Use voit v2.
func TestVault_RotateReplacesEncryptedValue(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)
	v.Set(context.Background(), "helloasso", "k", "v1", "admin") //nolint:errcheck

	if err := v.Rotate(context.Background(), "helloasso", "k", "v2", "admin"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	var got string
	v.Use(context.Background(), "helloasso", "k", func(p string) error { got = p; return nil }) //nolint:errcheck
	if got != "v2" {
		t.Errorf("post-rotate value = %q, attendu v2", got)
	}

	var rotatedAt sql.NullString
	db.QueryRow(`SELECT rotated_at FROM connector_credentials WHERE connector_id='helloasso' AND key_name='k'`).Scan(&rotatedAt)
	if !rotatedAt.Valid {
		t.Error("rotated_at non posé après Rotate")
	}
}

// TestVault_RotateAbsentReturnsError : Rotate sur clé non créée → erreur.
func TestVault_RotateAbsentReturnsError(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)
	err := v.Rotate(context.Background(), "helloasso", "absent", "v", "admin")
	if err == nil {
		t.Error("Rotate sur clé absente devrait retourner erreur")
	}
}

// TestVault_ListReturnsKeyNamesWithoutValues : List retourne les noms, pas les valeurs.
func TestVault_ListReturnsKeyNamesWithoutValues(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)
	v.Set(context.Background(), "helloasso", "client_id", "ID", "admin")     //nolint:errcheck
	v.Set(context.Background(), "helloasso", "client_secret", "SEC", "admin") //nolint:errcheck

	keys, err := v.List(context.Background(), "helloasso")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("List len=%d, attendu 2", len(keys))
	}
	for _, k := range keys {
		if k == "ID" || k == "SEC" {
			t.Errorf("List a leaké une valeur : %q", k)
		}
	}
}

// TestVault_PlaintextNeverLogged : Set/Use/Rotate, capture slog → plaintext jamais leaké.
func TestVault_PlaintextNeverLogged(t *testing.T) {
	db := openVaultDB(t)
	v, _ := NewVault(db, validHexKey)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	const secret = "BORIS_SUPER_SECRET_42"
	v.Set(context.Background(), "helloasso", "k", secret, "admin")            //nolint:errcheck
	v.Use(context.Background(), "helloasso", "k", func(_ string) error { return nil }) //nolint:errcheck
	v.Rotate(context.Background(), "helloasso", "k", secret+"_v2", "admin")   //nolint:errcheck

	if strings.Contains(buf.String(), secret) {
		t.Errorf("slog leaké le plaintext : %q dans %s", secret, buf.String())
	}
}

// TestVault_NewVaultMissingKeyFatal : NewVault("") retourne erreur typée.
func TestVault_NewVaultMissingKeyFatal(t *testing.T) {
	db := openVaultDB(t)
	if _, err := NewVault(db, ""); err == nil {
		t.Error("NewVault(\"\") devrait échouer")
	}
}

// TestVault_NewVaultInvalidLengthFatal : NewVault("0011") → erreur.
func TestVault_NewVaultInvalidLengthFatal(t *testing.T) {
	db := openVaultDB(t)
	if _, err := NewVault(db, "0011"); err == nil {
		t.Error("NewVault(short) devrait échouer")
	}
}
