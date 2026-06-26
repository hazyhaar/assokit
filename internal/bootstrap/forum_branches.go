package bootstrap

import (
	"context"
	"database/sql"
	"fmt"

	tree "github.com/hazyhaar/assokit/internal/nodetree"
	"github.com/hazyhaar/assokit/pkg/uid"
)

// BootstrapForumBranches crée, de façon idempotente, une branche "Général" (titrée
// comme la question) sous chaque QUESTION (nœud post de profondeur 2) qui n'a pas
// encore de branche enfant. Modèle pur question→branche→messages : sans branche, on
// ne peut pas répondre. Idempotent : ne touche que les questions sans enfant ; un
// re-run ne crée aucun doublon. Le slug est dérivé (branch-<uid>) — jamais
// slugify(titre) qui collisionnerait avec le slug de la question (même titre).
func BootstrapForumBranches(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title FROM nodes
		 WHERE depth=2 AND type='post' AND deleted_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM nodes c WHERE c.parent_id=nodes.id AND c.deleted_at IS NULL)`)
	if err != nil {
		return fmt.Errorf("BootstrapForumBranches select: %w", err)
	}
	type q struct{ id, title string }
	var todo []q
	for rows.Next() {
		var x q
		if err := rows.Scan(&x.id, &x.title); err != nil {
			rows.Close()
			return fmt.Errorf("BootstrapForumBranches scan: %w", err)
		}
		todo = append(todo, x)
	}
	rows.Close()

	store := &tree.Store{DB: db}
	for _, x := range todo {
		if _, err := store.Create(ctx, tree.Node{
			Slug:     "branch-" + uid.New(),
			ParentID: sql.NullString{String: x.id, Valid: true},
			Type:     "post",
			Title:    x.title,
		}); err != nil {
			return fmt.Errorf("BootstrapForumBranches create %s: %w", x.id, err)
		}
	}
	return nil
}
