package nodetree

import (
	"context"
	"database/sql"
	"fmt"
)

// Schema est le DDL SQLite de la table `nodes` exploitée par le moteur.
//
// Volontairement autonome : author_id est un TEXT libre (pas de clé étrangère
// vers une table d'utilisateurs), afin que le moteur reste agnostique. Un
// consommateur qui possède sa propre table users peut redéfinir ce schéma avec
// la contrainte d'intégrité de son choix — nodetree ne dépend que des colonnes
// présentes, pas de leurs contraintes.
const Schema = `
CREATE TABLE IF NOT EXISTS nodes (
  id            TEXT PRIMARY KEY,
  parent_id     TEXT REFERENCES nodes(id) ON DELETE CASCADE,
  slug          TEXT NOT NULL UNIQUE,
  type          TEXT NOT NULL CHECK(type IN ('folder','page','post','form','doc')),
  title         TEXT NOT NULL,
  body_md       TEXT NOT NULL DEFAULT '',
  body_html     TEXT NOT NULL DEFAULT '',
  visibility    TEXT NOT NULL DEFAULT 'public'
                CHECK(visibility IN ('public','members','role')),
  author_id     TEXT,
  display_order INTEGER NOT NULL DEFAULT 0,
  depth         INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at    TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id, display_order) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_nodes_slug   ON nodes(slug);
`

// Migrate applique le Schema (idempotent). Utile pour un consommateur standalone
// qui n'a pas son propre système de migrations. Les déploiements qui gèrent
// déjà la table `nodes` (ex : via leurs propres migrations) n'ont pas à l'appeler.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("nodetree.Migrate: %w", err)
	}
	return nil
}
