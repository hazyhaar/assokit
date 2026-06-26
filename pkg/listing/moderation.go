package listing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrReporterRequired est retourné quand un signalement est posé sans reporter_id.
var ErrReporterRequired = errors.New("listing: reporter_id requis")

// ErrReasonRequired est retourné quand un signalement est posé sans motif.
var ErrReasonRequired = errors.New("listing: motif de signalement requis")

// Report est un signalement d'annonce abusive émis par un membre. Le reporter
// est l'utilisateur authentifié à la bordure, jamais un paramètre.
type Report struct {
	ID         string
	ReporterID string
	ListingID  string
	Reason     string
	CreatedAt  time.Time
}

// migrateModeration crée la table de signalements et la colonne de masquage
// modérateur. Idempotent (CREATE TABLE IF NOT EXISTS + ADD COLUMN tolérant au
// doublon). Appelé depuis migrate.
//
// hidden est un FLAG de modération distinct du Status vendeur : un listing
// published+hidden reste published pour son owner mais disparaît de Search.
func (s *Store) migrateModeration(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listing_reports (
		id           TEXT NOT NULL PRIMARY KEY,
		reporter_id  TEXT NOT NULL,
		listing_id   TEXT NOT NULL,
		reason       TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create listing_reports: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_reports_listing ON listing_reports(listing_id)`); err != nil {
		return fmt.Errorf("index reports listing: %w", err)
	}

	// Colonne hidden : flag de modération. ADD COLUMN idempotent (SQLite ne
	// supporte pas IF NOT EXISTS avant 3.37 — on tente et on ignore le doublon).
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE listings ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("add column hidden: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_listings_hidden ON listings(hidden)`); err != nil {
		return fmt.Errorf("index listings hidden: %w", err)
	}
	return nil
}

// ReportListing persiste un signalement d'annonce. reporterID = identité
// authentifiée (jamais un paramètre). Retourne le Report créé (id alloué).
func (s *Store) ReportListing(ctx context.Context, reporterID, listingID, reason string) (*Report, error) {
	if strings.TrimSpace(reporterID) == "" {
		return nil, ErrReporterRequired
	}
	if strings.TrimSpace(listingID) == "" {
		return nil, ErrListingRequired
	}
	if strings.TrimSpace(reason) == "" {
		return nil, ErrReasonRequired
	}
	r := &Report{
		ID:         uid.New(),
		ReporterID: reporterID,
		ListingID:  listingID,
		Reason:     reason,
		CreatedAt:  time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO listing_reports (id, reporter_id, listing_id, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.ReporterID, r.ListingID, r.Reason, r.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("listing.ReportListing: %w", err)
	}
	return r, nil
}

// Hide masque un listing : il disparaît de Search quel que soit son Status,
// sans toucher au Status vendeur. Geste de modération. Retourne ErrNotFound si
// le listing n'existe pas. Idempotent (re-masquer un listing déjà masqué est
// sans effet).
func (s *Store) Hide(ctx context.Context, listingID string) error {
	return s.setHidden(ctx, listingID, true)
}

// Unhide retire le masquage de modération. Idempotent. Retourne ErrNotFound si
// le listing n'existe pas.
func (s *Store) Unhide(ctx context.Context, listingID string) error {
	return s.setHidden(ctx, listingID, false)
}

func (s *Store) setHidden(ctx context.Context, listingID string, hidden bool) error {
	if strings.TrimSpace(listingID) == "" {
		return ErrListingRequired
	}
	v := 0
	if hidden {
		v = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE listings SET hidden=? WHERE id=?`, v, listingID)
	if err != nil {
		return fmt.Errorf("listing.setHidden: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("listing.setHidden rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
