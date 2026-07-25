// CLAUDE:SUMMARY Auth assokit : Register/Authenticate bcrypt cost=12, cookie HMAC signé, erreurs typées.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("auth: email ou mot de passe incorrect")
	ErrEmailTaken         = errors.New("auth: email déjà utilisé")
	ErrUserInactive       = errors.New("auth: compte désactivé")
)

// ConfirmMailer est l'interface minimale pour l'envoi de confirmation post-inscription.
// Si nil dans Store, Register skip silencieusement l'envoi.
type ConfirmMailer interface {
	Enqueue(ctx context.Context, to, subject, bodyText, bodyHTML string) error
}

// User représente un utilisateur authentifié.
type User struct {
	ID          string
	Email       string
	DisplayName string
	IsActive    bool
	Roles       []string
	CreatedAt   time.Time
}

// Store est le dépôt d'authentification.
type Store struct {
	DB     *sql.DB
	Mailer ConfirmMailer
}

// Register crée un nouvel utilisateur avec mot de passe et le rôle member.
func (s *Store) Register(ctx context.Context, email, password, displayName string) (*User, error) {
	u, err := s.CreateAccount(ctx, CreateAccountOpts{
		Email:       email,
		DisplayName: displayName,
		Password:    password,
		Active:      true,
	})
	if err != nil {
		return nil, err
	}

	if s.Mailer != nil {
		_ = s.Mailer.Enqueue(ctx, u.Email,
			"Bienvenue sur Assokit",
			"Votre compte a bien été créé.",
			"<p>Votre compte a bien été créé.</p>",
		)
	}

	return u, nil
}

// Authenticate vérifie email+password et retourne l'utilisateur.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var id, hash, displayName string
	var isActive int
	var createdAt string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, password_hash, display_name, is_active, created_at FROM users WHERE email=?`, email,
	).Scan(&id, &hash, &displayName, &isActive, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("auth.Authenticate query: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if isActive == 0 {
		return nil, ErrUserInactive
	}

	roles, err := s.loadRoles(ctx, id)
	if err != nil {
		return nil, err
	}

	t, _ := time.Parse("2006-01-02 15:04:05", createdAt)
	return &User{ID: id, Email: email, DisplayName: displayName, IsActive: true, Roles: roles, CreatedAt: t}, nil
}

// GetByID charge un utilisateur avec ses rôles.
func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	var email, displayName, createdAt string
	var isActive int
	err := s.DB.QueryRowContext(ctx,
		`SELECT email, display_name, is_active, created_at FROM users WHERE id=?`, id,
	).Scan(&email, &displayName, &isActive, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.GetByID: %w", err)
	}

	roles, err := s.loadRoles(ctx, id)
	if err != nil {
		return nil, err
	}

	t, _ := time.Parse("2006-01-02 15:04:05", createdAt)
	return &User{ID: id, Email: email, DisplayName: displayName, IsActive: isActive == 1, Roles: roles, CreatedAt: t}, nil
}

// AddRole ajoute un rôle (= nom de grade : "admin", "member"…) à un utilisateur.
// RBAC migré : résout le nom vers grades.id puis écrit dans user_grades.
func (s *Store) AddRole(ctx context.Context, userID, roleID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_grades(user_id, grade_id)
		 SELECT ?, id FROM grades WHERE name = ? ON CONFLICT DO NOTHING`, userID, roleID)
	if err != nil {
		return fmt.Errorf("auth.AddRole: %w", err)
	}
	return nil
}

// RemoveRole retire un rôle (= nom de grade) d'un utilisateur.
func (s *Store) RemoveRole(ctx context.Context, userID, roleID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM user_grades WHERE user_id = ?
		 AND grade_id = (SELECT id FROM grades WHERE name = ?)`, userID, roleID)
	if err != nil {
		return fmt.Errorf("auth.RemoveRole: %w", err)
	}
	return nil
}

// loadRoles charge les rôles logiques de l'utilisateur depuis le RBAC migré
// (grades/user_grades, migration 00003). Le champ User.Roles porte les NOMS de
// grades (grades.name : "admin", "member"…), que tout le code consomme via
// slices.Contains(u.Roles, "admin"). L'ancienne table user_roles est morte.
//
// La résolution inclut la CLOSURE D'HÉRITAGE : un utilisateur dont un grade hérite
// d'« admin » porte aussi « admin » dans Roles. Sans cela, les gardes littérales
// (requireAdmin, requireAdminACL) ignoraient l'héritage et verrouillaient un
// administrateur via grade hérité (régression auditée 2026-06-13). Limite résiduelle
// assumée : un grade custom doté des permissions admin mais NON nommé « admin » ne
// passe pas les gardes par nom — le gating fin par permission (/admin/rbac) le couvre.
func (s *Store) loadRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`WITH RECURSIVE user_closure(grade_id) AS (
			SELECT grade_id FROM user_grades WHERE user_id = ?
			UNION
			SELECT gi.parent_id FROM grade_inherits gi
				JOIN user_closure uc ON gi.child_id = uc.grade_id
		)
		SELECT DISTINCT g.name FROM user_closure uc JOIN grades g ON g.id = uc.grade_id`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("auth.loadRoles: %w", err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
