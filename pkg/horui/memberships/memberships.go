// CLAUDE:SUMMARY Cotisations/adhésions membre : Store Create/ListForUser/ListAll/SetStatus/CurrentStatus. Montant centimes, statuts contraints. Import-clean (publiable).
package memberships

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrUserInvalid est retourné quand le membre cible n'existe pas ou est inactif.
var ErrUserInvalid = errors.New("memberships: membre introuvable ou inactif")

// ErrPeriodInvalid est retourné quand period_end n'est pas postérieur à period_start.
var ErrPeriodInvalid = errors.New("memberships: période invalide (fin <= début)")

// ErrStatusInvalid est retourné quand le statut n'est pas une valeur autorisée.
var ErrStatusInvalid = errors.New("memberships: statut invalide")

// ErrNotFound est retourné quand l'adhésion ciblée n'existe pas.
var ErrNotFound = errors.New("memberships: adhésion introuvable")

// validStatuses reflète la contrainte CHECK de la table memberships.
var validStatuses = map[string]struct{}{
	"pending":   {},
	"active":    {},
	"expired":   {},
	"cancelled": {},
}

// Membership est une adhésion / cotisation d'un membre sur une période donnée.
type Membership struct {
	ID          string
	UserID      string
	PeriodStart string
	PeriodEnd   string
	AmountCents int64
	Status      string
	Note        string
	CreatedAt   time.Time
}

// Store est le dépôt des adhésions.
type Store struct {
	DB *sql.DB
}

const tsLayout = "2006-01-02 15:04:05"

// Create enregistre une adhésion pour un membre. Valide que le membre existe et
// est actif, et que la période est cohérente (fin > début). Retourne l'ID créé.
func (s *Store) Create(ctx context.Context, m Membership) (string, error) {
	m.UserID = strings.TrimSpace(m.UserID)
	m.PeriodStart = strings.TrimSpace(m.PeriodStart)
	m.PeriodEnd = strings.TrimSpace(m.PeriodEnd)
	if m.PeriodStart == "" || m.PeriodEnd == "" || m.PeriodEnd <= m.PeriodStart {
		return "", ErrPeriodInvalid
	}
	if m.Status == "" {
		m.Status = "pending"
	}
	if _, ok := validStatuses[m.Status]; !ok {
		return "", ErrStatusInvalid
	}

	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, m.UserID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrUserInvalid
	}
	if err != nil {
		return "", fmt.Errorf("memberships.Create check user: %w", err)
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO memberships(id, user_id, period_start, period_end, amount_cents, status, note)
		 VALUES(?,?,?,?,?,?,?)`,
		id, m.UserID, m.PeriodStart, m.PeriodEnd, m.AmountCents, m.Status, m.Note,
	)
	if err != nil {
		return "", fmt.Errorf("memberships.Create: %w", err)
	}
	return id, nil
}

// ListForUser retourne les adhésions d'un membre, les plus récentes d'abord.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, period_start, period_end, amount_cents, status, note, created_at
		FROM memberships
		WHERE user_id=?
		ORDER BY period_start DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("memberships.ListForUser: %w", err)
	}
	defer rows.Close()
	return scanMemberships(rows)
}

// ListAll retourne toutes les adhésions, optionnellement filtrées par statut
// (statusFilter vide = pas de filtre). Les plus récentes d'abord.
func (s *Store) ListAll(ctx context.Context, statusFilter string) ([]Membership, error) {
	statusFilter = strings.TrimSpace(statusFilter)
	if statusFilter != "" {
		if _, ok := validStatuses[statusFilter]; !ok {
			return nil, ErrStatusInvalid
		}
	}
	query := `
		SELECT id, user_id, period_start, period_end, amount_cents, status, note, created_at
		FROM memberships
	`
	var args []any
	if statusFilter != "" {
		query += " WHERE status=?"
		args = append(args, statusFilter)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memberships.ListAll: %w", err)
	}
	defer rows.Close()
	return scanMemberships(rows)
}

// SetStatus change le statut d'une adhésion. Valide la valeur du statut.
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	if _, ok := validStatuses[status]; !ok {
		return ErrStatusInvalid
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE memberships SET status=? WHERE id=?`, status, id)
	if err != nil {
		return fmt.Errorf("memberships.SetStatus: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CurrentStatus retourne le statut de l'adhésion active courante d'un membre
// (status='active' et date du jour dans la période), sinon "none".
func (s *Store) CurrentStatus(ctx context.Context, userID string) (string, error) {
	today := time.Now().UTC().Format("2006-01-02")
	var status string
	err := s.DB.QueryRowContext(ctx, `
		SELECT status FROM memberships
		WHERE user_id=? AND status='active' AND period_start<=? AND period_end>=?
		ORDER BY period_end DESC
		LIMIT 1
	`, userID, today, today).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "none", nil
	}
	if err != nil {
		return "", fmt.Errorf("memberships.CurrentStatus: %w", err)
	}
	return status, nil
}

func scanMemberships(rows *sql.Rows) ([]Membership, error) {
	var result []Membership
	for rows.Next() {
		var m Membership
		var createdAt sql.NullString
		if err := rows.Scan(&m.ID, &m.UserID, &m.PeriodStart, &m.PeriodEnd, &m.AmountCents, &m.Status, &m.Note, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			m.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
