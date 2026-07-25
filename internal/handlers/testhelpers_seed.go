//go:build integration_cdp

package handlers

import (
	"context"
	"database/sql"

	tree "github.com/hazyhaar/nodetree"
)

// SeedNodes insère les nœuds nécessaires aux tests CDP.
// Idempotent : ignore les slug déjà présents.
// Seede aussi les permissions admin/member sur 'node-forum' pour que handleForumReply
// passe le middleware RequirePerm(PermWrite, 'node-forum').
func SeedNodes(db *sql.DB) error {
	store := &tree.Store{DB: db}
	ctx := context.Background()

	// Nœud racine forum — slug="forum" requis par handleForumIndex et /forum/{slug}.
	// Type 'folder' car schema CHECK(type IN ('folder','page','post','form','doc')).
	_, err := store.Create(ctx, tree.Node{
		Slug:  "forum",
		Type:  "folder",
		Title: "Forum général",
	})
	if err != nil && err != tree.ErrSlugTaken {
		return err
	}

	// Le route POST /forum/{slug}/reply utilise RequirePerm("node-forum") avec un
	// id littéral. Créer un node avec cet id et les permissions write pour admin/member.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO nodes(id, slug, type, title) VALUES('node-forum','node-forum','folder','Forum (perms anchor)')
		 ON CONFLICT(id) DO NOTHING`,
	); err != nil {
		return err
	}
	for _, role := range []string{"admin", "member"} {
		// node_permissions.role_id REFERENCES roles(id) : la row roles doit
		// exister avant l'insert (sinon FOREIGN KEY constraint failed).
		if _, err := db.ExecContext(ctx,
			`INSERT INTO roles(id, label) VALUES(?,?) ON CONFLICT(id) DO NOTHING`,
			role, role,
		); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO node_permissions(node_id, role_id, permission) VALUES(?,?,?)
			 ON CONFLICT DO NOTHING`,
			"node-forum", role, "write",
		); err != nil {
			return err
		}
	}
	return nil
}
