// CLAUDE:SUMMARY Module convention de pâturage AFP : Store Create/GetByID/ListAll/
// ListForUser/Revoke. DB injecté, single-writer base assokit. Import-clean (publiable).
package convention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrConventionNotFound est retourné quand la convention ciblée n'existe pas.
var ErrConventionNotFound = errors.New("convention: convention introuvable")

// ErrPreneurManquant est retourné quand l'identifiant du membre preneur est vide.
var ErrPreneurManquant = errors.New("convention: identifiant du preneur requis")

// ErrParcellesManquantes est retourné quand aucune parcelle n'est rattachée.
var ErrParcellesManquantes = errors.New("convention: au moins une parcelle requise")

// ErrDureeInvalide est retourné quand la durée n'est pas strictement positive.
var ErrDureeInvalide = errors.New("convention: durée en mois invalide")

// ErrStatutInvalide est retourné quand le statut n'est pas dans StatutsConvention.
var ErrStatutInvalide = errors.New("convention: statut invalide")

const tsLayout = "2006-01-02 15:04:05"

// Store est le dépôt du module convention. La connexion lui est injectée : il
// n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

var statutsValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(StatutsConvention))
	for _, st := range StatutsConvention {
		m[st] = struct{}{}
	}
	return m
}()

// Create enregistre une convention et ses parcelles rattachées dans une
// transaction unique, et retourne l'ID généré. La validation Go précède l'écriture
// (patron parcelle). Les FK étant activées en prod, une parcelle ou un preneur
// inconnu fait échouer l'insertion (fail-loud).
func (s *Store) Create(ctx context.Context, c Convention) (string, error) {
	c.PreneurID = strings.TrimSpace(c.PreneurID)
	if c.PreneurID == "" {
		return "", ErrPreneurManquant
	}
	parcelles := dedupNonEmpty(c.Parcelles)
	if len(parcelles) == 0 {
		return "", ErrParcellesManquantes
	}
	if c.DureeMois == 0 {
		c.DureeMois = DureeMoisDefaut
	}
	if c.DureeMois < 0 {
		return "", ErrDureeInvalide
	}
	if strings.TrimSpace(c.Statut) == "" {
		c.Statut = "brouillon"
	}
	if _, ok := statutsValides[c.Statut]; !ok {
		return "", ErrStatutInvalide
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("convention.Create begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id := uid.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO conventions(id, preneur_id, duree_mois, statut) VALUES(?,?,?,?)`,
		id, c.PreneurID, c.DureeMois, c.Statut,
	); err != nil {
		return "", fmt.Errorf("convention.Create: %w", err)
	}
	for _, pid := range parcelles {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO convention_parcelles(convention_id, parcelle_id) VALUES(?,?)`,
			id, pid,
		); err != nil {
			return "", fmt.Errorf("convention.Create lien parcelle %s: %w", pid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("convention.Create commit: %w", err)
	}
	return id, nil
}

// GetByID retourne une convention et ses parcelles rattachées. Retourne
// ErrConventionNotFound si l'identifiant est inconnu.
func (s *Store) GetByID(ctx context.Context, id string) (Convention, error) {
	var c Convention
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, preneur_id, duree_mois, statut, created_at
		FROM conventions WHERE id=?
	`, id).Scan(&c.ID, &c.PreneurID, &c.DureeMois, &c.Statut, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrConventionNotFound
	}
	if err != nil {
		return c, fmt.Errorf("convention.GetByID: %w", err)
	}
	if createdAt.Valid {
		c.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	parcelles, err := s.parcellesFor(ctx, s.DB, id)
	if err != nil {
		return c, err
	}
	c.Parcelles = parcelles
	return c, nil
}

// ListAll retourne toutes les conventions (vue bureau), les plus récentes
// d'abord, chacune décorée de ses parcelles.
func (s *Store) ListAll(ctx context.Context) ([]Convention, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, preneur_id, duree_mois, statut, created_at
		FROM conventions
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("convention.ListAll: %w", err)
	}
	return s.scanWithParcelles(ctx, rows)
}

// ListForUser retourne uniquement les conventions dont le membre est preneur. Le
// filtrage d'appartenance est applicatif (clause WHERE preneur_id).
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Convention, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, preneur_id, duree_mois, statut, created_at
		FROM conventions
		WHERE preneur_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("convention.ListForUser: %w", err)
	}
	return s.scanWithParcelles(ctx, rows)
}

// Revoke passe une convention au statut révoqué. Retourne ErrConventionNotFound
// si l'identifiant est inconnu. La révocation conserve la convention (trace), elle
// ne supprime rien.
func (s *Store) Revoke(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE conventions SET statut='revoquee' WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("convention.Revoke: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("convention.Revoke rows: %w", err)
	}
	if n == 0 {
		return ErrConventionNotFound
	}
	return nil
}

// scanWithParcelles lit les conventions du jeu de lignes puis charge les
// parcelles de chacune. Le jeu de lignes est fermé avant les requêtes de
// parcelles (single-writer base : pas de curseurs imbriqués sur la même conn).
func (s *Store) scanWithParcelles(ctx context.Context, rows *sql.Rows) ([]Convention, error) {
	var result []Convention
	func() {
		defer rows.Close()
		for rows.Next() {
			var c Convention
			var createdAt sql.NullString
			if err := rows.Scan(&c.ID, &c.PreneurID, &c.DureeMois, &c.Statut, &createdAt); err != nil {
				result = nil
				return
			}
			if createdAt.Valid {
				c.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
			}
			result = append(result, c)
		}
	}()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("convention.scan: %w", err)
	}
	for i := range result {
		parcelles, err := s.parcellesFor(ctx, s.DB, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Parcelles = parcelles
	}
	return result, nil
}

// parcellesFor retourne les identifiants de parcelles rattachées à une convention.
func (s *Store) parcellesFor(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, conventionID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT parcelle_id FROM convention_parcelles
		WHERE convention_id = ?
		ORDER BY parcelle_id
	`, conventionID)
	if err != nil {
		return nil, fmt.Errorf("convention.parcellesFor: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, fmt.Errorf("convention.parcellesFor scan: %w", err)
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

// dedupNonEmpty supprime les entrées vides et les doublons en préservant l'ordre.
func dedupNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
