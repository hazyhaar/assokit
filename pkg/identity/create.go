// Point unique de création de compte : Register (mot de passe) et magic-link /
// onboarding par activation (sans mot de passe) consomment la même voie.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
	"golang.org/x/crypto/bcrypt"
)

// CreateAccountOpts configure la création d'un compte utilisateur.
// Password vide = compte sans mot de passe (activation par lien ou magic-link).
// Active contrôle users.is_active (Register et magic-link : true).
type CreateAccountOpts struct {
	Email       string
	DisplayName string
	Password    string
	Active      bool
}

// CreateAccount crée un utilisateur, assigne le grade sys-member, et retourne
// l'utilisateur créé. ErrEmailTaken si l'adresse est déjà enregistrée.
func (s *Store) CreateAccount(ctx context.Context, opts CreateAccountOpts) (*User, error) {
	email := strings.ToLower(strings.TrimSpace(opts.Email))
	displayName := strings.TrimSpace(opts.DisplayName)
	if displayName == "" {
		displayName = email
	}

	var hash string
	if opts.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(opts.Password), 12)
		if err != nil {
			return nil, fmt.Errorf("identity.CreateAccount bcrypt: %w", err)
		}
		hash = string(h)
	}

	active := 0
	if opts.Active {
		active = 1
	}

	id := uid.New()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("identity.CreateAccount begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO users(id, email, password_hash, display_name, is_active, created_at) VALUES(?,?,?,?,?,?)`,
		id, email, hash, displayName, active, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("identity.CreateAccount insert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_grades(user_id, grade_id) VALUES(?,?) ON CONFLICT DO NOTHING`, id, "sys-member",
	); err != nil {
		return nil, fmt.Errorf("identity.CreateAccount grade: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("identity.CreateAccount commit: %w", err)
	}

	return &User{
		ID:          id,
		Email:       email,
		DisplayName: displayName,
		IsActive:    opts.Active,
		Roles:       []string{"member"},
	}, nil
}

// GetUserIDByEmail retourne l'identifiant d'un compte existant ou sql.ErrNoRows.
func (s *Store) GetUserIDByEmail(ctx context.Context, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// CreateAccountOrGet réutilise un compte existant (re-onboard) ou en crée un
// nouveau. Si l'email existe déjà, retourne l'utilisateur existant avec
// created=false — aucun doublon n'est inséré.
func (s *Store) CreateAccountOrGet(ctx context.Context, opts CreateAccountOpts) (*User, bool, error) {
	u, err := s.CreateAccount(ctx, opts)
	if err == nil {
		return u, true, nil
	}
	if !errors.Is(err, ErrEmailTaken) {
		return nil, false, err
	}
	id, qerr := s.GetUserIDByEmail(ctx, opts.Email)
	if qerr != nil {
		if qerr == sql.ErrNoRows {
			return nil, false, ErrEmailTaken
		}
		return nil, false, fmt.Errorf("identity.CreateAccountOrGet lookup: %w", qerr)
	}
	u2, gerr := s.GetByID(ctx, id)
	if gerr != nil || u2 == nil {
		return nil, false, fmt.Errorf("identity.CreateAccountOrGet get: %w", gerr)
	}
	return u2, false, nil
}
