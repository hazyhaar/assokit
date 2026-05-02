//go:build integration_cdp

package handlers

import (
	"context"
	"database/sql"

	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

// SeedNodes insère les nœuds nécessaires aux tests CDP.
// Idempotent : ignore les slug déjà présents.
func SeedNodes(db *sql.DB) error {
	store := &tree.Store{DB: db}
	ctx := context.Background()

	// Nœud racine forum — slug="forum" requis par handleForumIndex et /forum/{slug}.
	_, err := store.Create(ctx, tree.Node{
		Slug:  "forum",
		Type:  "forum",
		Title: "Forum général",
	})
	if err != nil && err != tree.ErrSlugTaken {
		return err
	}
	return nil
}
