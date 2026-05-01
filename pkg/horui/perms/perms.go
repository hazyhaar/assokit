// CLAUDE:SUMMARY Droits granulaires node×rôle avec héritage CTE récursive remontant parent_id.
package perms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Permission représente un niveau d'accès.
type Permission string

const (
	PermNone     Permission = "none"
	PermRead     Permission = "read"
	PermWrite    Permission = "write"
	PermModerate Permission = "moderate"
	PermAdmin    Permission = "admin"
)

// permLevel ordonne les permissions (plus grand = plus permissif).
var permLevel = map[Permission]int{
	PermNone:     0,
	PermRead:     1,
	PermWrite:    2,
	PermModerate: 3,
	PermAdmin:    4,
}

// AtLeast retourne true si p >= required.
func (p Permission) AtLeast(required Permission) bool {
	return permLevel[p] >= permLevel[required]
}

// max retourne la permission la plus permissive.
func max(a, b Permission) Permission {
	if permLevel[a] >= permLevel[b] {
		return a
	}
	return b
}

// Store est le dépôt de permissions.
type Store struct {
	DB *sql.DB
}

// Set pose ou met à jour la permission (nodeID, roleID) → p.
func (s *Store) Set(ctx context.Context, nodeID, roleID string, p Permission) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO node_permissions(node_id, role_id, permission) VALUES(?,?,?)
		 ON CONFLICT(node_id, role_id) DO UPDATE SET permission=excluded.permission`,
		nodeID, roleID, string(p),
	)
	if err != nil {
		return fmt.Errorf("perms.Set: %w", err)
	}
	return nil
}

// Unset supprime la permission explicite, laissant l'héritage reprendre.
func (s *Store) Unset(ctx context.Context, nodeID, roleID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM node_permissions WHERE node_id=? AND role_id=?`, nodeID, roleID)
	if err != nil {
		return fmt.Errorf("perms.Unset: %w", err)
	}
	return nil
}

// Get retourne la permission explicite (sans héritage). Retourne PermNone si absente.
func (s *Store) Get(ctx context.Context, nodeID, roleID string) (Permission, error) {
	var p string
	err := s.DB.QueryRowContext(ctx,
		`SELECT permission FROM node_permissions WHERE node_id=? AND role_id=?`,
		nodeID, roleID,
	).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return PermNone, nil
	}
	if err != nil {
		return PermNone, fmt.Errorf("perms.Get: %w", err)
	}
	return Permission(p), nil
}

// Effective résout la permission avec héritage : remonte les ancêtres jusqu'à trouver
// une row explicite. Si aucune trouvée jusqu'à la racine, retourne PermNone.
func (s *Store) Effective(ctx context.Context, nodeID, roleID string) (Permission, error) {
	// CTE récursive remontant parent_id jusqu'à la racine.
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE anc(id, parent_id, level) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE id=? AND deleted_at IS NULL
			UNION ALL
			SELECT n.id, n.parent_id, a.level+1
			FROM nodes n JOIN anc a ON n.id=a.parent_id
			WHERE n.deleted_at IS NULL
		)
		SELECT np.permission
		FROM anc
		JOIN node_permissions np ON np.node_id=anc.id AND np.role_id=?
		ORDER BY anc.level ASC
		LIMIT 1
	`, nodeID, roleID)
	if err != nil {
		return PermNone, fmt.Errorf("perms.Effective: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return PermNone, err
		}
		return Permission(p), nil
	}
	return PermNone, rows.Err()
}

// UserCan retourne true si au moins un des rôles de l'user a une permission ≥ required sur nodeID.
func (s *Store) UserCan(ctx context.Context, userRoles []string, nodeID string, required Permission) (bool, error) {
	best := PermNone
	for _, role := range userRoles {
		p, err := s.Effective(ctx, nodeID, role)
		if err != nil {
			return false, fmt.Errorf("perms.UserCan role %s: %w", role, err)
		}
		best = max(best, p)
		if best.AtLeast(required) {
			return true, nil // court-circuit
		}
	}
	return best.AtLeast(required), nil
}

// NodesUserCanRead retourne les IDs des nœuds où l'user (ses rôles) a au moins PermRead.
// Utilise une requête agrégée pour éviter N+1.
func (s *Store) NodesUserCanRead(ctx context.Context, userRoles []string) ([]string, error) {
	if len(userRoles) == 0 {
		return nil, nil
	}

	// Construit les placeholders
	placeholders := make([]interface{}, len(userRoles))
	ph := "?"
	for i, r := range userRoles {
		placeholders[i] = r
		if i > 0 {
			ph += ",?"
		}
	}

	// Récupère tous les nodes avec perm explicite pour ces rôles
	q := fmt.Sprintf(`
		SELECT DISTINCT np.node_id
		FROM node_permissions np
		WHERE np.role_id IN (%s)
		AND np.permission IN ('read','write','moderate','admin')
	`, ph)

	rows, err := s.DB.QueryContext(ctx, q, placeholders...)
	if err != nil {
		return nil, fmt.Errorf("perms.NodesUserCanRead: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
