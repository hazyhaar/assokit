// CLAUDE:SUMMARY Présence et représentation (V2b) : RecordMandate (pouvoir de
// représentation, 3 rejets typés), SignAttendance (émargement idempotent),
// ComputeQuorum (décompte sans double comptage, seuil paramétrable). Patron
// transactionnel V2a (execer partagé). Import-clean (publiable).
package governance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/hazyhaar/assokit/pkg/membership"
	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrMandatSurSoi est retourné quand mandant et mandataire sont la même personne :
// se représenter soi-même n'a pas de sens.
var ErrMandatSurSoi = errors.New("governance: mandant et mandataire identiques")

// ErrMandatExistant est retourné quand un mandat existe déjà pour ce mandant sur
// cette assemblée (au plus un mandat par mandant et par assemblée).
var ErrMandatExistant = errors.New("governance: un mandat existe déjà pour ce mandant sur cette assemblée")

// ErrMandantInactif est retourné quand le mandant n'est pas adhérent actif :
// seul un adhérent actif peut confier un pouvoir de représentation.
var ErrMandantInactif = errors.New("governance: le mandant n'est pas adhérent actif")

// ErrMandataireInactif est retourné quand le mandataire (proxy) n'est pas adhérent
// actif : un pouvoir ne peut être confié qu'à un autre membre adhérent actif.
var ErrMandataireInactif = errors.New("governance: le mandataire n'est pas adhérent actif")

// ErrAssembleeNonConvoquee est retourné quand un mandat ou un émargement est posé
// sur une assemblée qui n'est pas au statut convoquée (convoked) : on ne mandate ni
// n'émarge que sur une AG effectivement convoquée.
var ErrAssembleeNonConvoquee = errors.New("governance: l'assemblée n'est pas convoquée")

// statusChecker abstrait le contrôle « cet utilisateur est-il adhérent actif ? ».
// Défini côté consommateur (gouvernance), il est satisfait par *membership.Store
// (méthode CurrentStatus). Garde le contrôle d'éligibilité injectable et testable
// sans coupler RecordMandate à une implémentation concrète d'adhésion.
type statusChecker interface {
	CurrentStatus(ctx context.Context, userID string) (string, error)
}

// RecordMandate enregistre un pouvoir de représentation après contrôle de validité.
// Trois rejets typés précèdent toute écriture :
//   - ErrMandatSurSoi    : principal == proxy ;
//   - ErrMandantInactif  : le mandant n'est pas adhérent actif (membres.CurrentStatus) ;
//   - ErrMandatExistant   : un mandat existe déjà pour ce mandant sur cette assemblée.
//
// L'adhésion est résolue via *membership.Store (passé en argument), la gouvernance
// restant agnostique de la règle d'adhésion. L'unicité est garantie en base par la
// contrainte UNIQUE(assembly_id, principal_user_id) ; un conflit y est traduit en
// ErrMandatExistant (fail-loud).
func (s *Store) RecordMandate(ctx context.Context, members statusChecker, assemblyID, principalID, proxyID string) (string, error) {
	assemblyID = strings.TrimSpace(assemblyID)
	principalID = strings.TrimSpace(principalID)
	proxyID = strings.TrimSpace(proxyID)
	if principalID == proxyID {
		return "", ErrMandatSurSoi
	}

	// Garde d'état : on ne mandate que sur une AG convoquée (ni drafting ni cancelled).
	assembly, err := s.GetByID(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.RecordMandate assemblée: %w", err)
	}
	if assembly.Status != "convoked" {
		return "", ErrAssembleeNonConvoquee
	}

	status, err := members.CurrentStatus(ctx, principalID)
	if err != nil {
		return "", fmt.Errorf("governance.RecordMandate statut mandant: %w", err)
	}
	if status != "active" {
		return "", ErrMandantInactif
	}

	proxyStatus, err := members.CurrentStatus(ctx, proxyID)
	if err != nil {
		return "", fmt.Errorf("governance.RecordMandate statut mandataire: %w", err)
	}
	if proxyStatus != "active" {
		return "", ErrMandataireInactif
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO mandates(id, assembly_id, principal_user_id, proxy_user_id) VALUES(?,?,?,?)`,
		id, assemblyID, principalID, proxyID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrMandatExistant
		}
		return "", fmt.Errorf("governance.RecordMandate: %w", err)
	}
	return id, nil
}

// SignAttendance enregistre l'émargement d'un membre à une assemblée. Idempotent :
// un second appel sur le même couple (assembly_id, member_user_id) ne crée pas de
// doublon et ne renvoie pas d'erreur dure. La méthode documente le canal de
// signature (défaut "manual"). L'ID de la ligne d'émargement (créée ou préexistante)
// est retourné.
func (s *Store) SignAttendance(ctx context.Context, assemblyID, memberID, method string) (string, error) {
	assemblyID = strings.TrimSpace(assemblyID)
	memberID = strings.TrimSpace(memberID)
	method = strings.TrimSpace(method)
	if method == "" {
		method = "manual"
	}

	// Garde d'état : on n'émarge que sur une AG convoquée (ni drafting ni cancelled).
	assembly, err := s.GetByID(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.SignAttendance assemblée: %w", err)
	}
	if assembly.Status != "convoked" {
		return "", ErrAssembleeNonConvoquee
	}

	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO attendance(id, assembly_id, member_user_id, method) VALUES(?,?,?,?)
		 ON CONFLICT(assembly_id, member_user_id) DO NOTHING`,
		id, assemblyID, memberID, method,
	); err != nil {
		return "", fmt.Errorf("governance.SignAttendance: %w", err)
	}

	// Re-SELECT : l'INSERT a pu être ignoré (émargement préexistant) ; on retourne
	// l'ID réellement en base (l'ID généré n'est pertinent qu'au premier émargement).
	var existing string
	err = s.DB.QueryRowContext(ctx,
		`SELECT id FROM attendance WHERE assembly_id=? AND member_user_id=?`,
		assemblyID, memberID,
	).Scan(&existing)
	if err != nil {
		return "", fmt.Errorf("governance.SignAttendance reselect: %w", err)
	}
	return existing, nil
}

// PresentMemberIDs retourne l'ensemble des identifiants de membres ayant émargé à
// une assemblée. Sert à l'écran d'émargement pour afficher l'état présent/absent.
func (s *Store) PresentMemberIDs(ctx context.Context, assemblyID string) (map[string]struct{}, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT member_user_id FROM attendance WHERE assembly_id=?`, assemblyID)
	if err != nil {
		return nil, fmt.Errorf("governance.PresentMemberIDs: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("governance.PresentMemberIDs scan: %w", err)
		}
		out[m] = struct{}{}
	}
	return out, rows.Err()
}

// ComputeQuorum décompte le quorum d'une assemblée pour une population éligible
// passée par l'appelant. La gouvernance ne décide JAMAIS qui est éligible : la
// population (adhérents actifs à la date de l'AG) est calculée par le handler/action
// via membership.Store, puis fournie ici dans eligibleIDs.
//
// Règle de comptage :
//   - Eligible    = len(eligibleIDs) ;
//   - Present     = nombre d'émargés dont le membre est éligible ;
//   - Represented = nombre de mandats valides dont le mandant est éligible, NON déjà
//     présent, et dont le mandataire est lui-même éligible et présent ;
//   - Attending   = Present + Represented, sans double comptage (un mandant présent
//     n'est jamais aussi compté comme représenté) ;
//   - Reached     = Attending >= ceil(Eligible*threshold).
//
// threshold est un paramètre (aucune règle « moitié+1 » d'un type d'association codée
// en dur). Une AFP sans adhérent éligible (Eligible == 0) ne peut jamais atteindre le
// quorum, quel que soit threshold : un corps électoral vide ne délibère pas.
func (s *Store) ComputeQuorum(ctx context.Context, assemblyID string, eligibleIDs []string, threshold float64) (QuorumResult, error) {
	res := QuorumResult{Eligible: len(eligibleIDs), Threshold: threshold}

	eligible := make(map[string]struct{}, len(eligibleIDs))
	for _, id := range eligibleIDs {
		eligible[id] = struct{}{}
	}

	// Ensemble des présents éligibles (émargés).
	present := make(map[string]struct{})
	rows, err := s.DB.QueryContext(ctx,
		`SELECT member_user_id FROM attendance WHERE assembly_id=?`, assemblyID)
	if err != nil {
		return res, fmt.Errorf("governance.ComputeQuorum présents: %w", err)
	}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			return res, fmt.Errorf("governance.ComputeQuorum scan présent: %w", err)
		}
		if _, ok := eligible[m]; ok {
			present[m] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, fmt.Errorf("governance.ComputeQuorum présents err: %w", err)
	}
	rows.Close()
	res.Present = len(present)

	// Représentés : mandats dont le mandant est éligible, non déjà présent, et dont
	// le mandataire est éligible ET présent. Un même mandant n'est compté qu'une fois
	// (l'unicité en base garantit au plus un mandat par mandant et par AG).
	mrows, err := s.DB.QueryContext(ctx,
		`SELECT principal_user_id, proxy_user_id FROM mandates WHERE assembly_id=?`, assemblyID)
	if err != nil {
		return res, fmt.Errorf("governance.ComputeQuorum mandats: %w", err)
	}
	represented := make(map[string]struct{})
	for mrows.Next() {
		var principal, proxy string
		if err := mrows.Scan(&principal, &proxy); err != nil {
			mrows.Close()
			return res, fmt.Errorf("governance.ComputeQuorum scan mandat: %w", err)
		}
		if _, ok := eligible[principal]; !ok {
			continue // mandant non éligible
		}
		if _, ok := present[principal]; ok {
			continue // déjà présent → pas de double comptage
		}
		if _, ok := eligible[proxy]; !ok {
			continue // mandataire non éligible
		}
		if _, ok := present[proxy]; !ok {
			continue // mandataire absent → le pouvoir n'est pas exercé
		}
		represented[principal] = struct{}{}
	}
	if err := mrows.Err(); err != nil {
		mrows.Close()
		return res, fmt.Errorf("governance.ComputeQuorum mandats err: %w", err)
	}
	mrows.Close()
	res.Represented = len(represented)

	// Attending sans double comptage : present ∪ represented. Comme un mandant déjà
	// présent a été exclu de represented, les deux ensembles sont disjoints.
	res.Attending = res.Present + res.Represented

	needed := int(math.Ceil(float64(res.Eligible) * threshold))
	// Une AFP sans corps électoral ne peut pas atteindre le quorum : sans adhérent
	// éligible, aucune délibération n'est valide, même si Attending(0) >= needed(0)
	// le rendrait vacuously vrai. Le quorum exige au moins un éligible.
	res.Reached = res.Eligible > 0 && res.Attending >= needed
	return res, nil
}

// Garde de compilation : *membership.Store satisfait statusChecker. Conserve le
// contrat injecté même si la signature de CurrentStatus évolue côté adhésions.
var _ statusChecker = (*membership.Store)(nil)

// isUniqueViolation détecte une violation de contrainte UNIQUE remontée par
// modernc.org/sqlite, sans coupler la gouvernance au driver (inspection du message).
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
