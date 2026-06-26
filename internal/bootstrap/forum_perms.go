package bootstrap

import (
	"database/sql"
	"fmt"
)

// BootstrapForumPerms pose, de façon idempotente, les permissions d'écriture sur
// le forum pour les rôles « admin », « member » et « moderator » (un modérateur
// doit pouvoir répondre ; sans cette row, un sys-moderator est en 403 sur
// POST /forum/{slug}/reply — incident Dominique 2026-06-25, rétrogradé sys-admin
// → sys-moderator et privé d'écriture).
//
// Contexte : la route POST /forum/{slug}/reply est gardée par
// RequirePerm(PermWrite, "node-forum"). La résolution de permission (perms.Store)
// remonte les ancêtres du node d'id littéral "node-forum" dans node_permissions,
// dont la colonne role_id référence la table legacy roles(id). Les rôles portés
// par un utilisateur (User.Roles) sont les NOMS de grades — « admin », « member »
// (cf. seed 00004_rbac_system_grades_seed). Il faut donc, pour qu'un membre
// authentifié puisse répondre :
//   - un node d'id littéral "node-forum" servant d'ancre de permission ;
//   - une row roles(id) pour chaque rôle nommé « admin » et « member » (contrainte
//     de clé étrangère de node_permissions) ;
//   - une row node_permissions(node-forum, role, write) pour chacun.
//
// Sans ces rows (table roles et node_permissions vides au bootstrap initial),
// personne — Dominique inclus — ne peut répondre (403). Cette fonction est
// idempotente : tous les INSERT sont ON CONFLICT DO NOTHING, elle peut être
// appelée à chaque démarrage de l'instance.
func BootstrapForumPerms(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("BootstrapForumPerms begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Ancre de permission : node d'id littéral "node-forum" (≠ node de slug "forum",
	// auto-bootstrapé avec un id UUID). Type 'folder' imposé par le CHECK du schéma.
	if _, err := tx.Exec(
		`INSERT INTO nodes(id, slug, type, title) VALUES('node-forum','node-forum','folder','Forum (perms anchor)')
		 ON CONFLICT(id) DO NOTHING`,
	); err != nil {
		return fmt.Errorf("BootstrapForumPerms anchor node: %w", err)
	}

	for _, role := range []string{"admin", "member", "moderator"} {
		// roles(id) doit exister avant node_permissions (clé étrangère).
		if _, err := tx.Exec(
			`INSERT INTO roles(id, label) VALUES(?,?) ON CONFLICT(id) DO NOTHING`,
			role, role,
		); err != nil {
			return fmt.Errorf("BootstrapForumPerms role %s: %w", role, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO node_permissions(node_id, role_id, permission) VALUES(?,?,?)
			 ON CONFLICT(node_id, role_id) DO NOTHING`,
			"node-forum", role, "write",
		); err != nil {
			return fmt.Errorf("BootstrapForumPerms perm %s: %w", role, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("BootstrapForumPerms commit: %w", err)
	}
	return nil
}
