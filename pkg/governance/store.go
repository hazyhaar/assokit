// CLAUDE:SUMMARY Module gouvernance associative : Store Create/GetByID/ListAll des
// assemblées générales + MarkConvoked (transition drafting→convoked) et RecordConvocation
// (trace d'envoi). DB injecté, single-writer base assokit. Import-clean (publiable).
package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrAssemblyNotFound est retourné quand l'assemblée ciblée n'existe pas.
var ErrAssemblyNotFound = errors.New("governance: assemblée introuvable")

// ErrNameManquant est retourné quand le nom de l'assemblée est vide.
var ErrNameManquant = errors.New("governance: nom de l'assemblée requis")

// ErrStatutInvalide est retourné quand le statut n'est pas dans StatutsAssembly.
var ErrStatutInvalide = errors.New("governance: statut invalide")

// ErrPasConvocable est retourné quand une transition de convocation est demandée
// sur une assemblée qui n'est pas au statut brouillon (drafting).
var ErrPasConvocable = errors.New("governance: seule une assemblée en brouillon peut être convoquée")

const tsLayout = "2006-01-02 15:04:05"

// Store est le dépôt du module gouvernance. La connexion lui est injectée : il
// n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

// execer est l'exécuteur d'écritures commun à *sql.DB et *sql.Tx. Les écritures
// de transition + trace l'acceptent pour pouvoir partager une même transaction
// (atomicité de la convocation : AG convoked ⟺ traces complètes).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var statutsValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(StatutsAssembly))
	for _, st := range StatutsAssembly {
		m[st] = struct{}{}
	}
	return m
}()

// Create enregistre une assemblée générale et retourne l'ID généré. La validation
// Go précède l'écriture. Le statut par défaut est drafting (brouillon).
func (s *Store) Create(ctx context.Context, a Assembly) (string, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return "", ErrNameManquant
	}
	a.Agenda = strings.TrimSpace(a.Agenda)
	a.ScheduledAt = strings.TrimSpace(a.ScheduledAt)
	if strings.TrimSpace(a.Status) == "" {
		a.Status = "drafting"
	}
	if _, ok := statutsValides[a.Status]; !ok {
		return "", ErrStatutInvalide
	}

	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO assemblies(id, name, agenda, status, scheduled_at) VALUES(?,?,?,?,?)`,
		id, a.Name, a.Agenda, a.Status, a.ScheduledAt,
	); err != nil {
		return "", fmt.Errorf("governance.Create: %w", err)
	}
	return id, nil
}

// GetByID retourne une assemblée générale. Retourne ErrAssemblyNotFound si
// l'identifiant est inconnu.
func (s *Store) GetByID(ctx context.Context, id string) (Assembly, error) {
	var a Assembly
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, name, agenda, status, scheduled_at, created_at
		FROM assemblies WHERE id=?
	`, id).Scan(&a.ID, &a.Name, &a.Agenda, &a.Status, &a.ScheduledAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAssemblyNotFound
	}
	if err != nil {
		return a, fmt.Errorf("governance.GetByID: %w", err)
	}
	if createdAt.Valid {
		a.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	return a, nil
}

// ListAll retourne toutes les assemblées générales, les plus récentes d'abord.
func (s *Store) ListAll(ctx context.Context) ([]Assembly, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, agenda, status, scheduled_at, created_at
		FROM assemblies
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("governance.ListAll: %w", err)
	}
	defer rows.Close()
	var result []Assembly
	for rows.Next() {
		var a Assembly
		var createdAt sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &a.Agenda, &a.Status, &a.ScheduledAt, &createdAt); err != nil {
			return nil, fmt.Errorf("governance.ListAll scan: %w", err)
		}
		if createdAt.Valid {
			a.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// MarkConvoked passe une assemblée du statut brouillon (drafting) au statut
// convoquée (convoked) via la connexion injectée. Retourne ErrAssemblyNotFound si
// l'identifiant est inconnu, ErrPasConvocable si l'assemblée n'est pas en brouillon.
func (s *Store) MarkConvoked(ctx context.Context, id string) error {
	if err := s.MarkConvokedTx(ctx, s.DB, id); err != nil {
		// Sur la voie hors transaction, raffiner le diagnostic : distinguer une
		// assemblée inconnue d'un mauvais statut (une lecture séparée est sûre ici,
		// aucune transaction n'est ouverte).
		if errors.Is(err, ErrPasConvocable) {
			if _, gerr := s.GetByID(ctx, id); errors.Is(gerr, ErrAssemblyNotFound) {
				return ErrAssemblyNotFound
			}
		}
		return err
	}
	return nil
}

// MarkConvokedTx applique la transition brouillon (drafting) → convoquée (convoked)
// sur l'exécuteur fourni (*sql.DB ou *sql.Tx), permettant de l'inscrire dans une
// transaction partagée avec les traces de convocation. La transition est gardée par
// une clause WHERE status='drafting' (idempotence : un second appel échoue). En
// l'absence de ligne affectée, l'assemblée est soit inconnue, soit déjà hors
// brouillon : la résolution fine (ErrAssemblyNotFound vs ErrPasConvocable) reste à
// l'appelant, qui a déjà chargé l'assemblée ; ici, le défaut est ErrPasConvocable.
func (s *Store) MarkConvokedTx(ctx context.Context, x execer, id string) error {
	res, err := x.ExecContext(ctx,
		`UPDATE assemblies SET status='convoked' WHERE id=? AND status='drafting'`, id)
	if err != nil {
		return fmt.Errorf("governance.MarkConvoked: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("governance.MarkConvoked rows: %w", err)
	}
	if n == 0 {
		return ErrPasConvocable
	}
	return nil
}

// RecordConvocation insère une trace d'envoi de convocation pour un adhérent via la
// connexion injectée.
func (s *Store) RecordConvocation(ctx context.Context, assemblyID, userID string) error {
	return s.RecordConvocationTx(ctx, s.DB, assemblyID, userID)
}

// RecordConvocationTx insère une trace de convocation sur l'exécuteur fourni
// (*sql.DB ou *sql.Tx). Les FK étant activées en prod, une assemblée ou un
// utilisateur inconnu fait échouer l'insertion (fail-loud) — utile pour rendre la
// transaction de convocation atomique.
func (s *Store) RecordConvocationTx(ctx context.Context, x execer, assemblyID, userID string) error {
	id := uid.New()
	if _, err := x.ExecContext(ctx,
		`INSERT INTO convocations(id, assembly_id, user_id) VALUES(?,?,?)`,
		id, assemblyID, userID,
	); err != nil {
		return fmt.Errorf("governance.RecordConvocation: %w", err)
	}
	return nil
}
