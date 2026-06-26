// CLAUDE:SUMMARY Déclarations de mutation foncière (vente/succession) d'un membre
// propriétaire : Store Create/ListForUser/ListAll. DB injecté, single-writer base
// assokit. Aucune donnée fabriquée : tout vient du membre déclarant. Import-clean
// (publiable, ids via pkg/uid).
package mutation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrUserInvalid est retourné quand le membre déclarant n'existe pas ou est inactif.
var ErrUserInvalid = errors.New("mutation: membre introuvable ou inactif")

// ErrParcelleRefManquante est retourné quand la référence de parcelle est vide.
var ErrParcelleRefManquante = errors.New("mutation: référence de parcelle requise")

// ErrTypeInvalide est retourné quand le type de mutation n'est pas autorisé.
var ErrTypeInvalide = errors.New("mutation: type de mutation invalide")

// Types énumère les valeurs autorisées de Declaration.Type. Reflète la contrainte
// CHECK de la table account_mutations.
var Types = []string{"vente", "succession", "autre"}

var typesValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Types))
	for _, t := range Types {
		m[t] = struct{}{}
	}
	return m
}()

const tsLayout = "2006-01-02 15:04:05"

// Declaration est la déclaration par un propriétaire d'une mutation portant sur
// l'une de ses parcelles. Elle ne déclenche aucun transfert : c'est une trace.
type Declaration struct {
	ID          string
	UserID      string
	ParcelleRef string
	Type        string
	Details     string
	DeclaredAt  time.Time
	Status      string
}

// Store est le dépôt des déclarations de mutation. La connexion lui est injectée :
// il n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

// Create enregistre une déclaration de mutation pour un membre. Valide que le
// membre existe et est actif, que la référence de parcelle est présente et que le
// type est autorisé. Le statut initial est toujours « declaree ». Retourne l'ID créé.
func (s *Store) Create(ctx context.Context, d Declaration) (string, error) {
	d.UserID = strings.TrimSpace(d.UserID)
	d.ParcelleRef = strings.TrimSpace(d.ParcelleRef)
	d.Type = strings.TrimSpace(d.Type)
	d.Details = strings.TrimSpace(d.Details)

	if d.ParcelleRef == "" {
		return "", ErrParcelleRefManquante
	}
	if _, ok := typesValides[d.Type]; !ok {
		return "", ErrTypeInvalide
	}

	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, d.UserID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrUserInvalid
	}
	if err != nil {
		return "", fmt.Errorf("mutation.Create check user: %w", err)
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO account_mutations(id, user_id, parcelle_ref, type, details, status)
		 VALUES(?,?,?,?,?, 'declaree')`,
		id, d.UserID, d.ParcelleRef, d.Type, d.Details,
	)
	if err != nil {
		return "", fmt.Errorf("mutation.Create: %w", err)
	}
	return id, nil
}

// ListForUser retourne les déclarations d'un membre, les plus récentes d'abord.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Declaration, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, parcelle_ref, type, details, declared_at, status
		FROM account_mutations
		WHERE user_id=?
		ORDER BY declared_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("mutation.ListForUser: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// ListAll retourne toutes les déclarations (vue administration), les plus récentes
// d'abord.
func (s *Store) ListAll(ctx context.Context) ([]Declaration, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, parcelle_ref, type, details, declared_at, status
		FROM account_mutations
		ORDER BY declared_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("mutation.ListAll: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

func scan(rows *sql.Rows) ([]Declaration, error) {
	var result []Declaration
	for rows.Next() {
		var d Declaration
		var declaredAt sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.ParcelleRef, &d.Type, &d.Details, &declaredAt, &d.Status); err != nil {
			return nil, err
		}
		if declaredAt.Valid {
			d.DeclaredAt, _ = time.Parse(tsLayout, declaredAt.String)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
