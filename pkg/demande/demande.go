// CLAUDE:SUMMARY Demandes de mise à disposition d'un membre preneur : Store
// Create/ListForUser/ListAll/GetByID/SetStatut. DB injecté, single-writer base
// assokit. Statut initial forcé « soumise » côté serveur. Aucune donnée fabriquée :
// tout vient du membre demandeur. Import-clean (publiable, ids via pkg/uid).
package demande

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrUserInvalid est retourné quand le membre demandeur n'existe pas ou est inactif.
var ErrUserInvalid = errors.New("demande: membre introuvable ou inactif")

// ErrParcelleRefManquante est retourné quand la référence de parcelle est vide.
var ErrParcelleRefManquante = errors.New("demande: référence de parcelle requise")

// ErrObjetManquant est retourné quand l'objet/usage de la demande est vide.
var ErrObjetManquant = errors.New("demande: objet de la demande requis")

// ErrStatutInvalide est retourné quand le statut visé n'est pas une valeur autorisée.
var ErrStatutInvalide = errors.New("demande: statut invalide")

// ErrNotFound est retourné quand la demande ciblée n'existe pas.
var ErrNotFound = errors.New("demande: demande introuvable")

// ErrTransitionInterdite est retourné quand le statut courant n'autorise pas la
// transition demandée (ex. reconvertir une demande déjà convertie ou refusée).
var ErrTransitionInterdite = errors.New("demande: transition de statut interdite")

// Statuts énumère les valeurs autorisées de Demande.Statut. Reflète la contrainte
// CHECK de la table demande_mads.
const (
	StatutSoumise   = "soumise"
	StatutAcceptee  = "acceptee"
	StatutRefusee   = "refusee"
	StatutConvertie = "convertie"
)

// Statuts liste les statuts autorisés (validation des transitions).
var Statuts = []string{StatutSoumise, StatutAcceptee, StatutRefusee, StatutConvertie}

var statutsValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Statuts))
	for _, s := range Statuts {
		m[s] = struct{}{}
	}
	return m
}()

const tsLayout = "2006-01-02 15:04:05"

// Demande est la sollicitation par un membre preneur de la mise à disposition d'une
// parcelle. Elle ne déclenche aucun transfert : c'est une trace. Sa conversion en
// convention est opérée par l'administration via le parcours guidé de conversion.
type Demande struct {
	ID          string
	UserID      string
	ParcelleRef string
	Objet       string
	Details     string
	CreatedAt   time.Time
	Statut      string
}

// Store est le dépôt des demandes de mise à disposition. La connexion lui est
// injectée : il n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

// Create enregistre une demande pour un membre. Valide que le membre existe et est
// actif, que la référence de parcelle et l'objet sont présents. Le statut initial
// est toujours « soumise », forcé côté serveur (le formulaire ne le porte pas).
// Retourne l'ID créé.
func (s *Store) Create(ctx context.Context, d Demande) (string, error) {
	d.UserID = strings.TrimSpace(d.UserID)
	d.ParcelleRef = strings.TrimSpace(d.ParcelleRef)
	d.Objet = strings.TrimSpace(d.Objet)
	d.Details = strings.TrimSpace(d.Details)

	if d.ParcelleRef == "" {
		return "", ErrParcelleRefManquante
	}
	if d.Objet == "" {
		return "", ErrObjetManquant
	}

	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, d.UserID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrUserInvalid
	}
	if err != nil {
		return "", fmt.Errorf("demande.Create check user: %w", err)
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO demande_mads(id, user_id, parcelle_ref, objet, details, statut)
		 VALUES(?,?,?,?,?, 'soumise')`,
		id, d.UserID, d.ParcelleRef, d.Objet, d.Details,
	)
	if err != nil {
		return "", fmt.Errorf("demande.Create: %w", err)
	}
	return id, nil
}

// ListForUser retourne les demandes d'un membre, les plus récentes d'abord.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Demande, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, parcelle_ref, objet, details, created_at, statut
		FROM demande_mads
		WHERE user_id=?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("demande.ListForUser: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// ListAll retourne toutes les demandes (vue administration), les plus récentes
// d'abord.
func (s *Store) ListAll(ctx context.Context) ([]Demande, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, parcelle_ref, objet, details, created_at, statut
		FROM demande_mads
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("demande.ListAll: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// GetByID retourne une demande. Retourne ErrNotFound si l'identifiant est inconnu.
func (s *Store) GetByID(ctx context.Context, id string) (Demande, error) {
	var d Demande
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, user_id, parcelle_ref, objet, details, created_at, statut
		FROM demande_mads WHERE id=?
	`, id).Scan(&d.ID, &d.UserID, &d.ParcelleRef, &d.Objet, &d.Details, &createdAt, &d.Statut)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, fmt.Errorf("demande.GetByID: %w", err)
	}
	if createdAt.Valid {
		d.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	return d, nil
}

// SetStatut fait passer une demande à un nouveau statut, en validant la transition.
// Seule une demande « soumise » ou « acceptee » peut encore changer d'état : une
// demande « convertie » ou « refusee » est terminale (garde d'état contre une
// reconversion). Retourne ErrTransitionInterdite si la transition n'est pas permise,
// ErrNotFound si la demande est inconnue.
func (s *Store) SetStatut(ctx context.Context, id, statut string) error {
	if _, ok := statutsValides[statut]; !ok {
		return ErrStatutInvalide
	}
	// Transition atomique : la garde d'état repose sur la clause WHERE, pas sur un
	// pré-SELECT (deux conversions concurrentes ne peuvent plus passer toutes deux).
	// Une demande terminale (convertie/refusée) est exclue côté SQL.
	res, err := s.DB.ExecContext(ctx,
		`UPDATE demande_mads SET statut=? WHERE id=? AND statut NOT IN (?, ?)`,
		statut, id, StatutConvertie, StatutRefusee)
	if err != nil {
		return fmt.Errorf("demande.SetStatut: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("demande.SetStatut rows: %w", err)
	}
	if n == 0 {
		// Aucune ligne mutée : soit la demande n'existe pas (NotFound), soit son
		// statut est déjà terminal (TransitionInterdite). Le SELECT de diagnostic
		// n'est fait que dans ce cas froid.
		if _, err := s.GetByID(ctx, id); errors.Is(err, ErrNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		return ErrTransitionInterdite
	}
	return nil
}

func scan(rows *sql.Rows) ([]Demande, error) {
	var result []Demande
	for rows.Next() {
		var d Demande
		var createdAt sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.ParcelleRef, &d.Objet, &d.Details, &createdAt, &d.Statut); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			d.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
