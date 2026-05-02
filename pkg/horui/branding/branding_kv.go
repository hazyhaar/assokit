// CLAUDE:SUMMARY branding_kv — Get/Set/GetRow/DeleteFile/GetProgress avec cache sync.Map, invalidation après Set (M-ASSOKIT-ADMIN-PANEL-V0).
package branding

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// FieldDef définit un champ de branding avec son caractère obligatoire.
type FieldDef struct {
	Key      string
	Required bool
}

// ProgressInfo porte les compteurs de complétion pour required et all.
type ProgressInfo struct {
	RequiredTotal  int
	RequiredFilled int
	AllTotal       int
	AllFilled      int
}

var cache sync.Map // map[key]string

// Get lit la valeur d'une clé depuis le cache ou la DB. Retourne "" si absente.
func Get(db *sql.DB, key string) string {
	if v, ok := cache.Load(key); ok {
		return v.(string)
	}
	var value string
	err := db.QueryRow(`SELECT value FROM branding_kv WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return ""
	}
	cache.Store(key, value)
	return value
}

// Set insère ou remplace une entrée branding_kv et invalide le cache pour cette clé.
func Set(db *sql.DB, key, value, valueType, updatedBy string) error {
	_, err := db.Exec(
		`INSERT INTO branding_kv(key, value, value_type, updated_by, updated_at)
		 VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET
		   value      = excluded.value,
		   value_type = excluded.value_type,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at`,
		key, value, valueType, nullStr(updatedBy),
	)
	if err != nil {
		return fmt.Errorf("branding: set %q: %w", key, err)
	}
	cache.Delete(key)
	return nil
}

// Row représente une ligne branding_kv avec ses métadonnées.
type Row struct {
	Key       string
	Value     string
	ValueType string
	FilePath  string
	FileMime  string
	FileSize  int64
	UpdatedAt string
}

// GetRow retourne la ligne complète pour une clé, ou false si absente.
func GetRow(ctx context.Context, db *sql.DB, key string) (Row, bool) {
	var r Row
	err := db.QueryRowContext(ctx,
		`SELECT key, value, value_type, COALESCE(file_path,''), COALESCE(file_mime,''), file_size, updated_at FROM branding_kv WHERE key=?`,
		key).Scan(&r.Key, &r.Value, &r.ValueType, &r.FilePath, &r.FileMime, &r.FileSize, &r.UpdatedAt)
	return r, err == nil
}

// SetFile sauvegarde les métadonnées d'un fichier uploadé.
func SetFile(ctx context.Context, db *sql.DB, key, filePath, fileMime, updatedBy string, fileSize int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO branding_kv(key, value, value_type, file_path, file_mime, file_size, updated_by, updated_at)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, value_type=excluded.value_type,
		   file_path=excluded.file_path, file_mime=excluded.file_mime, file_size=excluded.file_size,
		   updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		key, filePath, "file", filePath, fileMime, fileSize, nullStr(updatedBy),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("branding: set-file %q: %w", key, err)
	}
	cache.Delete(key)
	return nil
}

// DeleteFile vide la valeur et file_path d'une entrée branding.
func DeleteFile(ctx context.Context, db *sql.DB, key string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE branding_kv SET value='', file_path=NULL, file_mime='', file_size=0, updated_at=? WHERE key=?`,
		time.Now().UTC().Format(time.RFC3339), key)
	cache.Delete(key)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// GetProgress compte les champs remplis parmi les FieldDef fournis.
func GetProgress(db *sql.DB, reg []FieldDef) ProgressInfo {
	if len(reg) == 0 {
		return ProgressInfo{}
	}

	// Récupérer toutes les valeurs non vides en une seule requête.
	keys := make([]any, len(reg))
	for i, f := range reg {
		keys[i] = f.Key
	}

	placeholders := ""
	for i := range keys {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}

	rows, err := db.Query(
		`SELECT key FROM branding_kv WHERE key IN (`+placeholders+`) AND value != ''`,
		keys...,
	)
	filled := map[string]bool{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				filled[k] = true
			}
		}
	}

	info := ProgressInfo{}
	for _, f := range reg {
		info.AllTotal++
		if filled[f.Key] {
			info.AllFilled++
		}
		if f.Required {
			info.RequiredTotal++
			if filled[f.Key] {
				info.RequiredFilled++
			}
		}
	}
	return info
}
