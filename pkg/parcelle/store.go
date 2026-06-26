// CLAUDE:SUMMARY Registre parcellaire AFP : Store Create/AddDroit/ListAll/ListForUser/Get. DB injecté, single-writer base assokit. Import-clean (publiable).
package parcelle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrParcelleNotFound est retourné quand la parcelle ciblée n'existe pas.
var ErrParcelleNotFound = errors.New("parcelle: parcelle introuvable")

// ErrParcelleExists est retourné quand la référence cadastrale
// (commune, section, numéro) est déjà enregistrée.
var ErrParcelleExists = errors.New("parcelle: référence cadastrale déjà enregistrée")

// ErrChampManquant est retourné quand un champ obligatoire de la référence
// cadastrale est vide.
var ErrChampManquant = errors.New("parcelle: commune, section et numéro requis")

// ErrNatureInvalide est retourné quand la nature du droit n'est pas autorisée.
var ErrNatureInvalide = errors.New("parcelle: nature du droit invalide")

// ErrStatutInvalide est retourné quand le statut de mise à disposition n'est pas
// dans l'énumération StatutsMad.
var ErrStatutInvalide = errors.New("parcelle: statut de mise à disposition invalide")

// ErrSurfaceInvalide est retourné quand la surface est négative.
var ErrSurfaceInvalide = errors.New("parcelle: surface négative")

const tsLayout = "2006-01-02 15:04:05"

// Store est le dépôt du registre parcellaire. La connexion lui est injectée :
// il n'ouvre jamais la sienne et ne lit aucune variable d'environnement.
type Store struct {
	DB *sql.DB
}

var naturesValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(NaturesDuDroit))
	for _, n := range NaturesDuDroit {
		m[n] = struct{}{}
	}
	return m
}()

var statutsValides = func() map[string]struct{} {
	m := make(map[string]struct{}, len(StatutsMad))
	for _, st := range StatutsMad {
		m[st] = struct{}{}
	}
	return m
}()

// Create enregistre une parcelle et retourne l'ID généré. Retourne
// ErrParcelleExists si la référence cadastrale est déjà présente.
func (s *Store) Create(ctx context.Context, p Parcelle) (string, error) {
	p.CommuneCode = strings.TrimSpace(p.CommuneCode)
	p.Section = strings.TrimSpace(p.Section)
	p.NumeroParcelle = strings.TrimSpace(p.NumeroParcelle)
	if p.CommuneCode == "" || p.Section == "" || p.NumeroParcelle == "" {
		return "", ErrChampManquant
	}
	if strings.TrimSpace(p.StatutMad) == "" {
		p.StatutMad = "libre"
	}
	if _, ok := statutsValides[p.StatutMad]; !ok {
		return "", ErrStatutInvalide
	}
	if p.SurfaceM2 < 0 {
		return "", ErrSurfaceInvalide
	}

	id := uid.New()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO parcelles(id, commune_code, section, numero_parcelle, surface_m2, statut_mad)
		 VALUES(?,?,?,?,?,?)`,
		id, p.CommuneCode, p.Section, p.NumeroParcelle, p.SurfaceM2, p.StatutMad,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", ErrParcelleExists
		}
		return "", fmt.Errorf("parcelle.Create: %w", err)
	}
	return id, nil
}

// AddDroit attache un droit (membre + nature) à une parcelle. Retourne l'ID du
// droit créé. La nature est seulement stockée, sans logique de vote associée.
func (s *Store) AddDroit(ctx context.Context, parcelleID, userID, nature string) (string, error) {
	parcelleID = strings.TrimSpace(parcelleID)
	userID = strings.TrimSpace(userID)
	if _, ok := naturesValides[nature]; !ok {
		return "", ErrNatureInvalide
	}

	var dummy string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM parcelles WHERE id=?`, parcelleID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrParcelleNotFound
	}
	if err != nil {
		return "", fmt.Errorf("parcelle.AddDroit check parcelle: %w", err)
	}

	id := uid.New()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO parcelle_droits(id, parcelle_id, user_id, nature_du_droit)
		 VALUES(?,?,?,?)`,
		id, parcelleID, userID, nature,
	)
	if err != nil {
		return "", fmt.Errorf("parcelle.AddDroit: %w", err)
	}
	return id, nil
}

// ListAll retourne toutes les parcelles (vue bureau), les plus récentes d'abord.
func (s *Store) ListAll(ctx context.Context) ([]Parcelle, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, commune_code, section, numero_parcelle, surface_m2, statut_mad, created_at
		FROM parcelles
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("parcelle.ListAll: %w", err)
	}
	defer rows.Close()
	return scanParcelles(rows)
}

// ListForUser retourne uniquement les parcelles sur lesquelles le membre détient
// un droit. Le filtrage d'appartenance est applicatif (clause WHERE sur
// parcelle_droits.user_id) : il n'est pas couvert par le contrôle d'accès par
// nœud, qui ne distingue pas les parcelles entre elles.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]Parcelle, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT p.id, p.commune_code, p.section, p.numero_parcelle, p.surface_m2, p.statut_mad, p.created_at
		FROM parcelles p
		JOIN parcelle_droits d ON d.parcelle_id = p.id
		WHERE d.user_id = ?
		ORDER BY p.created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("parcelle.ListForUser: %w", err)
	}
	defer rows.Close()
	return scanParcelles(rows)
}

// Get retourne une parcelle et ses droits. Retourne ErrParcelleNotFound si
// l'identifiant est inconnu.
func (s *Store) Get(ctx context.Context, id string) (ParcelleAvecDroits, error) {
	var pad ParcelleAvecDroits
	var createdAt sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, commune_code, section, numero_parcelle, surface_m2, statut_mad, created_at
		FROM parcelles WHERE id=?
	`, id).Scan(
		&pad.Parcelle.ID, &pad.Parcelle.CommuneCode, &pad.Parcelle.Section,
		&pad.Parcelle.NumeroParcelle, &pad.Parcelle.SurfaceM2, &pad.Parcelle.StatutMad, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return pad, ErrParcelleNotFound
	}
	if err != nil {
		return pad, fmt.Errorf("parcelle.Get: %w", err)
	}
	if createdAt.Valid {
		pad.Parcelle.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, parcelle_id, user_id, nature_du_droit, created_at
		FROM parcelle_droits WHERE parcelle_id=?
		ORDER BY created_at
	`, id)
	if err != nil {
		return pad, fmt.Errorf("parcelle.Get droits: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d Droit
		var dCreated sql.NullString
		if err := rows.Scan(&d.ID, &d.ParcelleID, &d.UserID, &d.NatureDuDroit, &dCreated); err != nil {
			return pad, fmt.Errorf("parcelle.Get scan droit: %w", err)
		}
		if dCreated.Valid {
			d.CreatedAt, _ = time.Parse(tsLayout, dCreated.String)
		}
		pad.Droits = append(pad.Droits, d)
	}
	return pad, rows.Err()
}

func scanParcelles(rows *sql.Rows) ([]Parcelle, error) {
	var result []Parcelle
	for rows.Next() {
		var p Parcelle
		var createdAt sql.NullString
		if err := rows.Scan(&p.ID, &p.CommuneCode, &p.Section, &p.NumeroParcelle, &p.SurfaceM2, &p.StatutMad, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			p.CreatedAt, _ = time.Parse(tsLayout, createdAt.String)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
