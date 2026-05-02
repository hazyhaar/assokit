// CLAUDE:SUMMARY Migration runner SQLite : applique initial.sql v1 + migrations/v2_feedbacks.sql v2 via go:embed, idempotent via schema_version.
package chassis

import (
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed initial.sql
var initialSQL string

//go:embed migrations/v2_feedbacks.sql
var v2FeedbacksSQL string

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

	if err := applyMigration(db, 1, initialSQL); err != nil {
		return err
	}
	if err := applyMigration(db, 2, v2FeedbacksSQL); err != nil {
		return err
	}
	return nil
}

// applyMigration applique le SQL d'une migration si elle n'a pas encore été appliquée.
func applyMigration(db *sql.DB, version int, sqlStr string) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = ?`, version).Scan(&count); err != nil {
		return fmt.Errorf("schema: check v%d: %w", version, err)
	}
	if count > 0 {
		return nil // déjà appliqué
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("schema: begin v%d: %w", version, err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(sqlStr); err != nil {
		return fmt.Errorf("schema: apply v%d: %w", version, err)
	}

	if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES(?)`, version); err != nil {
		return fmt.Errorf("schema: record v%d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schema: commit v%d: %w", version, err)
	}
	return nil
}
