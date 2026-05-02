// CLAUDE:SUMMARY Vault des credentials connectors — Set/Use/Rotate/List avec AES-GCM (M-ASSOKIT-SPRINT2-S2).
// CLAUDE:WARN Use callback reçoit plaintext slice qui est ZÉRO-OUT après return (défense memory dump). Ne pas garder de référence.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Vault gère les credentials chiffrés des connectors.
type Vault struct {
	DB        *sql.DB
	MasterKey []byte // 32 bytes
}

// NewVault construit un Vault à partir du master key hex (env NPS_MASTER_KEY).
// Erreur fatale si key absente / mal formée — rule MASTER-KEY-FATAL-IF-MISSING.
func NewVault(db *sql.DB, masterKeyHex string) (*Vault, error) {
	key, err := DecodeMasterKey(masterKeyHex)
	if err != nil {
		return nil, err
	}
	return &Vault{DB: db, MasterKey: key}, nil
}

// Set chiffre value et UPSERT en DB.
// byActor : userID qui a setté (pour audit), peut être vide si bootstrap.
func (v *Vault) Set(ctx context.Context, connectorID, keyName, value, byActor string) error {
	if connectorID == "" || keyName == "" {
		return errors.New("Vault.Set: connectorID et keyName requis")
	}
	encrypted, err := Encrypt(v.MasterKey, []byte(value))
	if err != nil {
		return fmt.Errorf("Vault.Set encrypt: %w", err)
	}
	var actor any
	if byActor != "" {
		actor = byActor
	}
	_, err = v.DB.ExecContext(ctx, `
		INSERT INTO connector_credentials(connector_id, key_name, encrypted_value, set_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(connector_id, key_name) DO UPDATE SET
			encrypted_value = excluded.encrypted_value,
			set_at = CURRENT_TIMESTAMP,
			set_by = excluded.set_by
	`, connectorID, keyName, encrypted, actor)
	if err != nil {
		return fmt.Errorf("Vault.Set db: %w", err)
	}
	return nil
}

// Use lit + déchiffre + appelle callback avec plaintext.
// Le slice plaintext est zero-out après le return de callback.
func (v *Vault) Use(ctx context.Context, connectorID, keyName string, callback func(plaintext string) error) error {
	var encrypted []byte
	err := v.DB.QueryRowContext(ctx,
		`SELECT encrypted_value FROM connector_credentials WHERE connector_id = ? AND key_name = ?`,
		connectorID, keyName,
	).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("Vault.Use: credential %q/%q absent", connectorID, keyName)
	}
	if err != nil {
		return fmt.Errorf("Vault.Use db: %w", err)
	}
	plaintext, err := Decrypt(v.MasterKey, encrypted)
	if err != nil {
		return fmt.Errorf("Vault.Use decrypt: %w", err)
	}
	defer ZeroBytes(plaintext)
	return callback(string(plaintext))
}

// Rotate remplace le credential par newValue. Marque rotated_at.
func (v *Vault) Rotate(ctx context.Context, connectorID, keyName, newValue, byActor string) error {
	encrypted, err := Encrypt(v.MasterKey, []byte(newValue))
	if err != nil {
		return fmt.Errorf("Vault.Rotate encrypt: %w", err)
	}
	var actor any
	if byActor != "" {
		actor = byActor
	}
	res, err := v.DB.ExecContext(ctx, `
		UPDATE connector_credentials
		SET encrypted_value = ?, set_at = CURRENT_TIMESTAMP, set_by = ?, rotated_at = CURRENT_TIMESTAMP
		WHERE connector_id = ? AND key_name = ?
	`, encrypted, actor, connectorID, keyName)
	if err != nil {
		return fmt.Errorf("Vault.Rotate db: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("Vault.Rotate: credential %q/%q absent (utiliser Set pour créer)", connectorID, keyName)
	}
	return nil
}

// List retourne les noms de clés présents pour un connector. Pas les valeurs.
func (v *Vault) List(ctx context.Context, connectorID string) ([]string, error) {
	rows, err := v.DB.QueryContext(ctx,
		`SELECT key_name FROM connector_credentials WHERE connector_id = ? ORDER BY key_name`,
		connectorID,
	)
	if err != nil {
		return nil, fmt.Errorf("Vault.List db: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
