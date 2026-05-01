// CLAUDE:SUMMARY Auth NPS : Register/Authenticate bcrypt cost=12, cookie HMAC signé, erreurs typées.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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

// Register crée un nouvel utilisateur avec le rôle 'member' par défaut.
func (s *Store) Register(ctx context.Context, email, password, displayName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("auth.Register bcrypt: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth.Register begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO users(id, email, password_hash, display_name, is_active, created_at) VALUES(?,?,?,?,1,?)`,
		id, email, string(hash), displayName, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("auth.Register insert: %w", err)
	}

	// Assigne le rôle 'member' par défaut
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_roles(user_id, role_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, "member",
	); err != nil {
		return nil, fmt.Errorf("auth.Register role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth.Register commit: %w", err)
	}

	if s.Mailer != nil {
		_ = s.Mailer.Enqueue(ctx, email,
			"Bienvenue",
			"Votre compte a bien été créé.",
			"<p>Votre compte a bien été créé.</p>",
		)
	}

	return &User{ID: id, Email: email, DisplayName: displayName, IsActive: true, Roles: []string{"member"}}, nil
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

// AddRole ajoute un rôle à un utilisateur.
func (s *Store) AddRole(ctx context.Context, userID, roleID string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_roles(user_id, role_id) VALUES(?,?) ON CONFLICT DO NOTHING`, userID, roleID)
	if err != nil {
		return fmt.Errorf("auth.AddRole: %w", err)
	}
	return nil
}

// RemoveRole retire un rôle d'un utilisateur.
func (s *Store) RemoveRole(ctx context.Context, userID, roleID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM user_roles WHERE user_id=? AND role_id=?`, userID, roleID)
	if err != nil {
		return fmt.Errorf("auth.RemoveRole: %w", err)
	}
	return nil
}

func (s *Store) loadRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT role_id FROM user_roles WHERE user_id=?`, userID)
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
