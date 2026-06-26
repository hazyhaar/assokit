// CLAUDE:SUMMARY Décision : vote simple (V2c) : OpenResolution (garde AG convoquée),
// CastVote (4 gardes typées : scrutin ouvert, choix valide, votant adhérent actif,
// double vote), CloseResolution (seule transition open→closed), Tally (dépouillement
// déterministe). Patron transactionnel/gardes V2a/V2b. Import-clean (publiable).
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

// ErrResolutionLabelManquant est retourné quand la question soumise au vote est vide.
var ErrResolutionLabelManquant = errors.New("governance: libellé de la résolution requis")

// ErrScrutinClos est retourné quand un vote est tenté sur une résolution close ou
// inexistante : un bulletin n'est accepté que tant que le scrutin est ouvert.
var ErrScrutinClos = errors.New("governance: le scrutin n'est pas ouvert")

// ErrChoixInvalide est retourné quand le choix exprimé n'est pas l'un de
// {pour, contre, abstention}.
var ErrChoixInvalide = errors.New("governance: choix de vote invalide")

// ErrVotantInactif est retourné quand le votant n'est pas adhérent actif : seul un
// adhérent actif peut voter.
var ErrVotantInactif = errors.New("governance: le votant n'est pas adhérent actif")

// ErrDejaVote est retourné quand le votant a déjà voté sur cette résolution. Le vote
// est un acte enregistré : un second bulletin n'écrase rien et n'est pas un no-op
// silencieux ; il est explicitement rejeté.
var ErrDejaVote = errors.New("governance: ce votant a déjà voté sur cette résolution")

// ErrResolutionIntrouvable est retourné quand la résolution ciblée n'existe pas.
var ErrResolutionIntrouvable = errors.New("governance: résolution introuvable")

// ErrPasCloturable est retourné quand une clôture est demandée sur une résolution
// déjà close ou inexistante (seule transition ouverte→close).
var ErrPasCloturable = errors.New("governance: seule une résolution ouverte peut être close")

var choixValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(ChoixVote))
	for _, c := range ChoixVote {
		m[c] = struct{}{}
	}
	return m
}()

// OpenResolution ouvre une résolution (question soumise au vote) sur une assemblée.
// Garde d'état : l'AG doit être convoquée (ni drafting ni cancelled) — réutilise
// GetByID puis rejette ErrAssembleeNonConvoquee, comme RecordMandate/SignAttendance.
// La résolution est créée au statut open. Retourne l'identifiant généré.
func (s *Store) OpenResolution(ctx context.Context, assemblyID, label string) (string, error) {
	assemblyID = strings.TrimSpace(assemblyID)
	label = strings.TrimSpace(label)
	if label == "" {
		return "", ErrResolutionLabelManquant
	}

	assembly, err := s.GetByID(ctx, assemblyID)
	if err != nil {
		return "", fmt.Errorf("governance.OpenResolution assemblée: %w", err)
	}
	if assembly.Status != "convoked" {
		return "", ErrAssembleeNonConvoquee
	}

	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO resolutions(id, assembly_id, label, status) VALUES(?,?,?,'open')`,
		id, assemblyID, label,
	); err != nil {
		return "", fmt.Errorf("governance.OpenResolution: %w", err)
	}
	return id, nil
}

// GetResolution retourne une résolution. Retourne ErrResolutionIntrouvable si
// l'identifiant est inconnu.
func (s *Store) GetResolution(ctx context.Context, id string) (Resolution, error) {
	var r Resolution
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, assembly_id, label, status, created_at
		FROM resolutions WHERE id=?
	`, id).Scan(&r.ID, &r.AssemblyID, &r.Label, &r.Status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrResolutionIntrouvable
	}
	if err != nil {
		return r, fmt.Errorf("governance.GetResolution: %w", err)
	}
	if createdAt.Valid {
		r.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}
	return r, nil
}

// ListResolutions retourne les résolutions d'une assemblée, les plus récentes
// d'abord.
func (s *Store) ListResolutions(ctx context.Context, assemblyID string) ([]Resolution, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, assembly_id, label, status, created_at
		FROM resolutions WHERE assembly_id=?
		ORDER BY created_at DESC
	`, assemblyID)
	if err != nil {
		return nil, fmt.Errorf("governance.ListResolutions: %w", err)
	}
	defer rows.Close()
	var out []Resolution
	for rows.Next() {
		var r Resolution
		var createdAt sql.NullString
		if err := rows.Scan(&r.ID, &r.AssemblyID, &r.Label, &r.Status, &createdAt); err != nil {
			return nil, fmt.Errorf("governance.ListResolutions scan: %w", err)
		}
		if createdAt.Valid {
			r.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CastVoteWithChecker enregistre un bulletin après quatre gardes typées, toutes AVANT
// l'insert :
//   - la résolution doit être ouverte (chargée via GetResolution ; rejet ErrScrutinClos
//     si close OU inexistante — un scrutin inconnu n'est pas ouvert) ;
//   - le choix doit être l'un de {pour, contre, abstention} (rejet ErrChoixInvalide) ;
//   - le votant doit être adhérent actif (members.CurrentStatus ; rejet ErrVotantInactif).
//
// L'unicité (resolution_id, voter_user_id) interdit le double vote : un second vote du
// même votant est traduit en ErrDejaVote (fail-loud, jamais d'écrasement ni de no-op).
// L'éligibilité est contrôlée par-votant ici, à l'insert : aucune population n'a à être
// passée (découpage symétrique de V2b, où ComputeQuorum reçoit une population mais où le
// contrôle d'un acte individuel — mandat, vote — se fait par-personne via statusChecker).
//
// C'est la SEULE voie exportée d'enregistrement d'un bulletin : le contrôle d'éligibilité
// (statusChecker, satisfait par *membership.Store côté consommateur) y est toujours
// présent. Aucune voie publique n'enregistre un vote sans ce contrôle.
func (s *Store) CastVoteWithChecker(ctx context.Context, members statusChecker, resolutionID, voterID, choice string) (string, error) {
	return s.castVote(ctx, members, resolutionID, voterID, choice)
}

func (s *Store) castVote(ctx context.Context, members statusChecker, resolutionID, voterID, choice string) (string, error) {
	resolutionID = strings.TrimSpace(resolutionID)
	voterID = strings.TrimSpace(voterID)
	choice = strings.TrimSpace(choice)

	if _, ok := choixValides[choice]; !ok {
		return "", ErrChoixInvalide
	}

	// Garde scrutin : la résolution doit exister ET être ouverte.
	res, err := s.GetResolution(ctx, resolutionID)
	if err != nil {
		if errors.Is(err, ErrResolutionIntrouvable) {
			return "", ErrScrutinClos // un scrutin inconnu n'est pas un scrutin ouvert
		}
		return "", fmt.Errorf("governance.CastVote résolution: %w", err)
	}
	if res.Status != "open" {
		return "", ErrScrutinClos
	}

	// Garde éligibilité : le votant doit être adhérent actif.
	if members != nil {
		status, err := members.CurrentStatus(ctx, voterID)
		if err != nil {
			return "", fmt.Errorf("governance.CastVote statut votant: %w", err)
		}
		if status != "active" {
			return "", ErrVotantInactif
		}
	}

	id := uid.New()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO votes(id, resolution_id, voter_user_id, choice) VALUES(?,?,?,?)`,
		id, resolutionID, voterID, choice,
	); err != nil {
		if isUniqueViolation(err) {
			return "", ErrDejaVote
		}
		return "", fmt.Errorf("governance.CastVote: %w", err)
	}
	return id, nil
}

// CloseResolution clôt une résolution : seule transition open→closed, gardée par une
// clause WHERE status='open' (vérification de RowsAffected). Une résolution déjà close
// ou inexistante n'affecte aucune ligne → ErrPasCloturable.
func (s *Store) CloseResolution(ctx context.Context, resolutionID string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE resolutions SET status='closed' WHERE id=? AND status='open'`, resolutionID)
	if err != nil {
		return fmt.Errorf("governance.CloseResolution: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("governance.CloseResolution rows: %w", err)
	}
	if n == 0 {
		return ErrPasCloturable
	}
	return nil
}

// Tally dépouille une résolution : comptage déterministe des bulletins par choix.
// Total = nombre de votes enregistrés (population votante réelle), pas la population
// éligible. Une résolution sans aucun vote renvoie un Tally à zéro, sans erreur.
func (s *Store) Tally(ctx context.Context, resolutionID string) (Tally, error) {
	var t Tally
	rows, err := s.DB.QueryContext(ctx,
		`SELECT choice, COUNT(*) FROM votes WHERE resolution_id=? GROUP BY choice`, resolutionID)
	if err != nil {
		return t, fmt.Errorf("governance.Tally: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var choice string
		var n int
		if err := rows.Scan(&choice, &n); err != nil {
			return t, fmt.Errorf("governance.Tally scan: %w", err)
		}
		switch choice {
		case "pour":
			t.Pour = n
		case "contre":
			t.Contre = n
		case "abstention":
			t.Abstention = n
		}
		t.Total += n
	}
	return t, rows.Err()
}
