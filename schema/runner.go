// CLAUDE:SUMMARY Migration runner SQLite : applique initial.sql v1 via go:embed, idempotent via schema_version.
package schema

import (
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed initial.sql
var initialSQL string

// Run applique toutes les migrations non encore appliquées.
// Idempotent : vérifie schema_version avant chaque migration.
// Chaque migration est atomique (transaction complète ou rollback).
func Run(db *sql.DB) error {
	// Créer schema_version si absente (premier boot avant toute migration).
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	) STRICT`)
	if err != nil {
		return fmt.Errorf("schema: create schema_version: %w", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 1`).Scan(&count); err != nil {
		return fmt.Errorf("schema: check v1: %w", err)
	}
	if count > 0 {
		return nil // déjà appliqué
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("schema: begin v1: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(initialSQL); err != nil {
		return fmt.Errorf("schema: apply v1: %w", err)
	}

	if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES(1)`); err != nil {
		return fmt.Errorf("schema: record v1: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schema: commit v1: %w", err)
	}
	return nil
}
