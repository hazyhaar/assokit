// CLAUDE:SUMMARY Migration runner SQLite UNIQUE : applique initial.sql (v1) + migrations/v2_feedbacks.sql (v2) + migrations/later/*.sql (v3+) via go:embed, idempotent via schema_version. Aucun état global (plus de goose).
package chassis

import (
	"database/sql"
	"embed"
	_ "embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed initial.sql
var initialSQL string

//go:embed migrations/v2_feedbacks.sql
var v2FeedbacksSQL string

// migrations/later contient les migrations v3+ (préfixe numérique NNNNNN_nom.sql).
// Format historique goose toléré : la section « -- +goose Down » est ignorée,
// seul l'Up est appliqué. Plus aucune dépendance à goose ni à son état global
// (ce qui élimine la data race des api.New() concurrents — chantier S1).
//
//go:embed migrations/later
var laterMigrationsFS embed.FS

const laterDir = "migrations/later"

// Run applique toutes les migrations non encore appliquées, dans l'ordre.
// Idempotent : vérifie schema_version avant chaque migration.
// Chaque migration est atomique (transaction complète ou rollback).
// Aucun état global de package n'est muté → sûr en api.New() concurrents.
func Run(db *sql.DB) error {
	// Créer schema_version si absente (premier boot avant toute migration).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	) STRICT`); err != nil {
		return fmt.Errorf("schema: create schema_version: %w", err)
	}

	// Compat : une DB déjà migrée par l'ancien double-runner traçait les
	// migrations v3+ dans schema_version_goose. On reporte cet historique dans
	// schema_version pour ne pas ré-appliquer des migrations non idempotentes
	// (ex : 00009 DROP TABLE signups).
	if err := backfillLegacyGooseHistory(db); err != nil {
		return err
	}

	if err := applyMigration(db, 1, initialSQL); err != nil {
		return err
	}
	if err := applyMigration(db, 2, v2FeedbacksSQL); err != nil {
		return err
	}

	migs, err := loadLaterMigrations()
	if err != nil {
		return err
	}
	for _, m := range migs {
		if err := applyMigration(db, m.version, m.sql); err != nil {
			return err
		}
	}
	return nil
}

type laterMigration struct {
	version int
	sql     string
}

// loadLaterMigrations lit migrations/later, trie par nom de fichier et extrait
// la section Up de chaque migration. La version = préfixe numérique du fichier.
func loadLaterMigrations() ([]laterMigration, error) {
	entries, err := fs.ReadDir(laterMigrationsFS, laterDir)
	if err != nil {
		return nil, fmt.Errorf("schema: read later migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	migs := make([]laterMigration, 0, len(names))
	for _, name := range names {
		version, err := parseVersionPrefix(name)
		if err != nil {
			return nil, fmt.Errorf("schema: %s: %w", name, err)
		}
		content, err := fs.ReadFile(laterMigrationsFS, laterDir+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("schema: read %s: %w", name, err)
		}
		migs = append(migs, laterMigration{version: version, sql: extractUp(string(content))})
	}
	return migs, nil
}

// parseVersionPrefix extrait le préfixe numérique d'un nom « 00009_xxx.sql » → 9.
func parseVersionPrefix(name string) (int, error) {
	prefix := name
	if i := strings.IndexByte(name, '_'); i >= 0 {
		prefix = name[:i]
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("préfixe de version invalide: %w", err)
	}
	if v < 3 {
		return 0, fmt.Errorf("version %d réservée (v1/v2 = initial.sql/v2_feedbacks.sql)", v)
	}
	return v, nil
}

// extractUp retourne le SQL applicable : tout ce qui précède « -- +goose Down »,
// l'éventuel marqueur « -- +goose Up » étant un commentaire inoffensif.
func extractUp(content string) string {
	if i := strings.Index(content, "-- +goose Down"); i >= 0 {
		content = content[:i]
	}
	return content
}

// backfillLegacyGooseHistory reporte les versions appliquées de l'ancienne table
// goose (schema_version_goose) dans schema_version. No-op si la table est absente.
func backfillLegacyGooseHistory(db *sql.DB) error {
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version_goose'`).Scan(&name)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("schema: detect legacy goose table: %w", err)
	}

	rows, err := db.Query(
		`SELECT version_id FROM schema_version_goose WHERE is_applied = 1 AND version_id >= 3`)
	if err != nil {
		return fmt.Errorf("schema: read legacy goose history: %w", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("schema: scan legacy version: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("schema: iterate legacy history: %w", err)
	}

	for _, v := range versions {
		if _, err := db.Exec(`INSERT OR IGNORE INTO schema_version(version) VALUES(?)`, v); err != nil {
			return fmt.Errorf("schema: backfill v%d: %w", v, err)
		}
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
