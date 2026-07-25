// CLAUDE:SUMMARY Demandes d'octroi de profil métier : Store Create/List/Get/SetStatut
// et détection de doublon soumis. Table profile_requests dédiée (O2), distincte de
// demande_mads. DB injecté, import-clean (UUIDv7 via google/uuid).
package profilrequest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrUserInvalid est retourné quand le membre demandeur n'existe pas ou est inactif.
var ErrUserInvalid = errors.New("profilrequest: membre introuvable ou inactif")

// ErrGradeIDManquant est retourné quand l'identifiant de grade est vide.
var ErrGradeIDManquant = errors.New("profilrequest: identifiant de grade requis")

// ErrDejaSoumise est retourné quand une demande soumise existe déjà pour le couple.
var ErrDejaSoumise = errors.New("profilrequest: une demande soumise existe déjà pour ce profil")

const tsLayout = "2006-01-02 15:04:05"

// Request est une demande d'octroi de profil métier émise par un membre.
type Request struct {
	ID        string
	UserID    string
	GradeID   string
	Statut    string
	CreatedAt time.Time
}

// Store est le dépôt des demandes de profil. La connexion lui est injectée : il
// n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

// Create enregistre une demande soumise pour un membre et un grade. Refuse si une
// demande soumise existe déjà pour le même couple (user_id, grade_id).
func (s *Store) Create(ctx context.Context, userID, gradeID string) (string, error) {
	userID = strings.TrimSpace(userID)
	gradeID = strings.TrimSpace(gradeID)
	if gradeID == "" {
		return "", ErrGradeIDManquant
	}

	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, userID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrUserInvalid
	}
	if err != nil {
		return "", fmt.Errorf("profilrequest.Create check user: %w", err)
	}

	exists, err := s.ExistsSoumise(ctx, userID, gradeID)
	if err != nil {
		return "", err
	}
	if exists {
		return "", ErrDejaSoumise
	}

	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("profilrequest.Create id: %w", err)
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO profile_requests(id, user_id, grade_id, statut) VALUES(?,?,?, 'soumise')`,
		id.String(), userID, gradeID,
	)
	if err != nil {
		return "", fmt.Errorf("profilrequest.Create: %w", err)
	}
	return id.String(), nil
}

// ExistsSoumise indique si une demande au statut « soumise » existe pour le couple.
func (s *Store) ExistsSoumise(ctx context.Context, userID, gradeID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM profile_requests
		WHERE user_id=? AND grade_id=? AND statut='soumise'
	`, userID, gradeID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("profilrequest.ExistsSoumise: %w", err)
	}
	return n > 0, nil
}

// ListByStatut retourne les demandes au statut donné, les plus récentes d'abord.
func (s *Store) ListByStatut(ctx context.Context, statut string) ([]Request, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, grade_id, statut, created_at
		FROM profile_requests
		WHERE statut=?
		ORDER BY created_at DESC
	`, statut)
	if err != nil {
		return nil, fmt.Errorf("profilrequest.ListByStatut: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// ListForUser retourne les demandes d'un membre, les plus récentes d'abord.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Request, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, grade_id, statut, created_at
		FROM profile_requests
		WHERE user_id=?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("profilrequest.ListForUser: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// GetByID retourne une demande par identifiant, ou nil si absente.
func (s *Store) GetByID(ctx context.Context, id string) (*Request, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, user_id, grade_id, statut, created_at
		FROM profile_requests WHERE id=?
	`, id)
	req, err := scanOne(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profilrequest.GetByID: %w", err)
	}
	return req, nil
}

// SetStatut met à jour le statut d'une demande. Utilise la connexion ou la
// transaction fournie via execer (DB ou *sql.Tx).
func (s *Store) SetStatut(ctx context.Context, id, statut string) error {
	return s.setStatutExec(ctx, s.DB, id, statut)
}

// SetStatutTx met à jour le statut dans une transaction en cours.
func (s *Store) SetStatutTx(ctx context.Context, tx *sql.Tx, id, statut string) error {
	return s.setStatutExec(ctx, tx, id, statut)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) setStatutExec(ctx context.Context, ex execer, id, statut string) error {
	_, err := ex.ExecContext(ctx,
		`UPDATE profile_requests SET statut=? WHERE id=?`,
		statut, id,
	)
	if err != nil {
		return fmt.Errorf("profilrequest.SetStatut: %w", err)
	}
	return nil
}

func scan(rows *sql.Rows) ([]Request, error) {
	var result []Request
	for rows.Next() {
		req, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, req)
	}
	return result, rows.Err()
}

func scanOne(row *sql.Row) (*Request, error) {
	var req Request
	var createdAt sql.NullString
	if err := row.Scan(&req.ID, &req.UserID, &req.GradeID, &req.Statut, &createdAt); err != nil {
		return nil, err
	}
	if createdAt.Valid {
		req.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	return &req, nil
}

func scanRow(scanner interface {
	Scan(dest ...any) error
}) (Request, error) {
	var req Request
	var createdAt sql.NullString
	if err := scanner.Scan(&req.ID, &req.UserID, &req.GradeID, &req.Statut, &createdAt); err != nil {
		return Request{}, err
	}
	if createdAt.Valid {
		req.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	return req, nil
}
