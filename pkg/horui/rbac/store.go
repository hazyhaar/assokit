// CLAUDE:SUMMARY RBAC store : permissions, grades, héritage DAG (cycle DFS), assignation user, audit trail.
package rbac

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("rbac: not found")
	ErrSystemGrade   = errors.New("rbac: cannot delete a system grade")
	ErrCycleDetected = errors.New("rbac: inheritance cycle detected")
)

type Store struct {
	DB *sql.DB
}

type Permission struct {
	ID          string
	Name        string
	Description string
}

type Grade struct {
	ID     string
	Name   string
	System bool
}

// EnsurePermission crée la permission si elle n'existe pas encore, retourne son ID.
func (s *Store) EnsurePermission(ctx context.Context, name, description string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM permissions WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("rbac: ensure permission %q: %w", name, err)
	}
	id = uuid.New().String()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO permissions(id, name, description) VALUES(?, ?, ?)`, id, name, description)
	if err != nil {
		return "", fmt.Errorf("rbac: insert permission %q: %w", name, err)
	}
	s.audit(ctx, "ensure_permission", "", id, name)
	return id, nil
}

// ListPermissions retourne toutes les permissions.
func (s *Store) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, description FROM permissions ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list permissions: %w", err)
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateGrade crée un grade non-système.
func (s *Store) CreateGrade(ctx context.Context, name string) (string, error) {
	id := uuid.New().String()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO grades(id, name, system) VALUES(?, ?, 0)`, id, name)
	if err != nil {
		return "", fmt.Errorf("rbac: create grade %q: %w", name, err)
	}
	s.audit(ctx, "create_grade", "", id, name)
	return id, nil
}

// DeleteGrade supprime un grade non-système.
func (s *Store) DeleteGrade(ctx context.Context, gradeID string) error {
	var system int
	err := s.DB.QueryRowContext(ctx, `SELECT system FROM grades WHERE id = ?`, gradeID).Scan(&system)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("rbac: delete grade check: %w", err)
	}
	if system == 1 {
		return ErrSystemGrade
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM grades WHERE id = ?`, gradeID); err != nil {
		return fmt.Errorf("rbac: delete grade: %w", err)
	}
	s.audit(ctx, "delete_grade", "", gradeID, "")
	return nil
}

// ListGrades retourne tous les grades.
func (s *Store) ListGrades(ctx context.Context) ([]Grade, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, system FROM grades ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list grades: %w", err)
	}
	defer rows.Close()
	var out []Grade
	for rows.Next() {
		var g Grade
		var sys int
		if err := rows.Scan(&g.ID, &g.Name, &sys); err != nil {
			return nil, err
		}
		g.System = sys == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGrade retourne un grade par ID.
func (s *Store) GetGrade(ctx context.Context, gradeID string) (*Grade, error) {
	var g Grade
	var sys int
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, system FROM grades WHERE id = ?`, gradeID).Scan(&g.ID, &g.Name, &sys)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("rbac: get grade: %w", err)
	}
	g.System = sys == 1
	return &g, nil
}

// GrantPerm attribue une permission à un grade.
func (s *Store) GrantPerm(ctx context.Context, gradeID, permID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO grade_permissions(grade_id, permission_id) VALUES(?, ?)`,
		gradeID, permID)
	if err != nil {
		return fmt.Errorf("rbac: grant perm: %w", err)
	}
	s.audit(ctx, "grant_perm", gradeID, permID, "")
	return nil
}

// RevokePerm retire une permission d'un grade.
func (s *Store) RevokePerm(ctx context.Context, gradeID, permID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM grade_permissions WHERE grade_id = ? AND permission_id = ?`,
		gradeID, permID)
	if err != nil {
		return fmt.Errorf("rbac: revoke perm: %w", err)
	}
	s.audit(ctx, "revoke_perm", gradeID, permID, "")
	return nil
}

// GradePermissions retourne les permissions directes d'un grade.
func (s *Store) GradePermissions(ctx context.Context, gradeID string) ([]Permission, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT p.id, p.name, p.description
		FROM permissions p
		JOIN grade_permissions gp ON gp.permission_id = p.id
		WHERE gp.grade_id = ?
		ORDER BY p.name`, gradeID)
	if err != nil {
		return nil, fmt.Errorf("rbac: grade permissions: %w", err)
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddInherit ajoute une relation d'héritage child → parent.
// Retourne ErrCycleDetected si child est déjà ancêtre de parent (cycle).
func (s *Store) AddInherit(ctx context.Context, childID, parentID string) error {
	ancestors, err := s.GradeAncestors(ctx, parentID)
	if err != nil {
		return err
	}
	for _, a := range ancestors {
		if a == childID {
			return ErrCycleDetected
		}
	}
	if childID == parentID {
		return ErrCycleDetected
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO grade_inherits(child_id, parent_id) VALUES(?, ?)`,
		childID, parentID)
	if err != nil {
		return fmt.Errorf("rbac: add inherit: %w", err)
	}
	s.audit(ctx, "add_inherit", childID, parentID, "")
	return nil
}

// RemoveInherit supprime une relation d'héritage.
func (s *Store) RemoveInherit(ctx context.Context, childID, parentID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM grade_inherits WHERE child_id = ? AND parent_id = ?`,
		childID, parentID)
	if err != nil {
		return fmt.Errorf("rbac: remove inherit: %w", err)
	}
	s.audit(ctx, "remove_inherit", childID, parentID, "")
	return nil
}

// GradeAncestors retourne tous les ancêtres (transitifs) d'un grade via CTE récursive.
func (s *Store) GradeAncestors(ctx context.Context, gradeID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE ancestors(id) AS (
			SELECT parent_id FROM grade_inherits WHERE child_id = ?
			UNION
			SELECT gi.parent_id FROM grade_inherits gi
			JOIN ancestors a ON gi.child_id = a.id
		)
		SELECT id FROM ancestors`, gradeID)
	if err != nil {
		return nil, fmt.Errorf("rbac: grade ancestors: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AssignGrade assigne un grade à un utilisateur.
func (s *Store) AssignGrade(ctx context.Context, userID, gradeID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_grades(user_id, grade_id) VALUES(?, ?)`,
		userID, gradeID)
	if err != nil {
		return fmt.Errorf("rbac: assign grade: %w", err)
	}
	s.audit(ctx, "assign_grade", userID, gradeID, "")
	return nil
}

// RemoveGrade retire un grade d'un utilisateur.
func (s *Store) RemoveGrade(ctx context.Context, userID, gradeID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM user_grades WHERE user_id = ? AND grade_id = ?`,
		userID, gradeID)
	if err != nil {
		return fmt.Errorf("rbac: remove grade: %w", err)
	}
	s.audit(ctx, "remove_grade", userID, gradeID, "")
	return nil
}

// UserGrades retourne les grades assignés à un utilisateur.
func (s *Store) UserGrades(ctx context.Context, userID string) ([]Grade, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT g.id, g.name, g.system
		FROM grades g
		JOIN user_grades ug ON ug.grade_id = g.id
		WHERE ug.user_id = ?
		ORDER BY g.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: user grades: %w", err)
	}
	defer rows.Close()
	var out []Grade
	for rows.Next() {
		var g Grade
		var sys int
		if err := rows.Scan(&g.ID, &g.Name, &sys); err != nil {
			return nil, err
		}
		g.System = sys == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) audit(ctx context.Context, action, actorID, targetID, detail string) {
	id := uuid.New().String()
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO rbac_audit(id, action, actor_id, target_id, detail) VALUES(?, ?, ?, ?, ?)`,
		id, action, actorID, targetID, detail)
}
