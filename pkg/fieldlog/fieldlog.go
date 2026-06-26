// CLAUDE:SUMMARY Journal de terrain d'un membre preneur : entretien réalisé ou
// problème terrain observé (deux variantes, champ Kind). Store Create/ListForUser/
// ListForUserKind/ListAll. DB injecté, single-writer base assokit. Aucune donnée
// fabriquée : tout vient du membre déclarant. Import-clean (publiable, ids pkg/uid).
package fieldlog

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
var ErrUserInvalid = errors.New("fieldlog: membre introuvable ou inactif")

// ErrKindInvalide est retourné quand le type d'entrée n'est ni entretien ni problème.
var ErrKindInvalide = errors.New("fieldlog: type d'entrée invalide")

// ErrCategorieInvalide est retourné quand la catégorie n'appartient pas à celles
// autorisées pour le type d'entrée fourni.
var ErrCategorieInvalide = errors.New("fieldlog: catégorie invalide pour ce type d'entrée")

// Kinds énumère les deux variantes de saisie. Reflète la contrainte CHECK(kind).
const (
	KindEntretien = "entretien"
	KindProbleme  = "probleme"
)

// CategoriesEntretien énumère les catégories autorisées d'un entretien réalisé.
var CategoriesEntretien = []string{"cloture", "eau", "debroussaillage", "autre"}

// CategoriesProbleme énumère les catégories autorisées d'un problème terrain.
var CategoriesProbleme = []string{"gibier", "divagation", "acces", "danger", "autre"}

var categoriesParKind = map[string]map[string]struct{}{
	KindEntretien: setOf(CategoriesEntretien),
	KindProbleme:  setOf(CategoriesProbleme),
}

func setOf(values []string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[v] = struct{}{}
	}
	return m
}

const tsLayout = "2006-01-02 15:04:05"

// Entry est une entrée du journal de terrain d'un membre : un entretien réalisé ou
// un problème observé, distingués par Kind. Elle ne déclenche aucun traitement :
// c'est une trace consultable par le bureau.
type Entry struct {
	ID          string
	UserID      string
	Kind        string
	Category    string
	ParcelleRef string
	EventDate   string
	Details     string
	CreatedAt   time.Time
}

// Store est le dépôt du journal de terrain. La connexion lui est injectée : il
// n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

// Create enregistre une entrée du journal de terrain pour un membre. Valide que le
// membre existe et est actif, que le type d'entrée est connu et que la catégorie est
// autorisée pour ce type. Retourne l'ID créé.
func (s *Store) Create(ctx context.Context, e Entry) (string, error) {
	e.UserID = strings.TrimSpace(e.UserID)
	e.Kind = strings.TrimSpace(e.Kind)
	e.Category = strings.TrimSpace(e.Category)
	e.ParcelleRef = strings.TrimSpace(e.ParcelleRef)
	e.EventDate = strings.TrimSpace(e.EventDate)
	e.Details = strings.TrimSpace(e.Details)

	cats, ok := categoriesParKind[e.Kind]
	if !ok {
		return "", ErrKindInvalide
	}
	if _, ok := cats[e.Category]; !ok {
		return "", ErrCategorieInvalide
	}

	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, e.UserID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrUserInvalid
	}
	if err != nil {
		return "", fmt.Errorf("fieldlog.Create check user: %w", err)
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO field_logs(id, user_id, kind, category, parcelle_ref, event_date, details)
		 VALUES(?,?,?,?,?,?,?)`,
		id, e.UserID, e.Kind, e.Category, e.ParcelleRef, e.EventDate, e.Details,
	)
	if err != nil {
		return "", fmt.Errorf("fieldlog.Create: %w", err)
	}
	return id, nil
}

// ListForUser retourne toutes les entrées d'un membre, les plus récentes d'abord.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, kind, category, parcelle_ref, event_date, details, created_at
		FROM field_logs
		WHERE user_id=?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("fieldlog.ListForUser: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// ListForUserKind retourne les entrées d'un membre d'un type donné (entretien ou
// problème), les plus récentes d'abord.
func (s *Store) ListForUserKind(ctx context.Context, userID, kind string) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, kind, category, parcelle_ref, event_date, details, created_at
		FROM field_logs
		WHERE user_id=? AND kind=?
		ORDER BY created_at DESC
	`, userID, kind)
	if err != nil {
		return nil, fmt.Errorf("fieldlog.ListForUserKind: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// ListAll retourne toutes les entrées (vue administration), les plus récentes
// d'abord. Un filtre de type facultatif (kind vide = toutes catégories) restreint
// la vue.
func (s *Store) ListAll(ctx context.Context, kind string) ([]Entry, error) {
	kind = strings.TrimSpace(kind)
	var (
		rows *sql.Rows
		err  error
	)
	if kind == "" {
		rows, err = s.DB.QueryContext(ctx, `
			SELECT id, user_id, kind, category, parcelle_ref, event_date, details, created_at
			FROM field_logs
			ORDER BY created_at DESC
		`)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
			SELECT id, user_id, kind, category, parcelle_ref, event_date, details, created_at
			FROM field_logs
			WHERE kind=?
			ORDER BY created_at DESC
		`, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("fieldlog.ListAll: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

func scan(rows *sql.Rows) ([]Entry, error) {
	var result []Entry
	for rows.Next() {
		var e Entry
		var createdAt sql.NullString
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &e.Category, &e.ParcelleRef, &e.EventDate, &e.Details, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			e.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
