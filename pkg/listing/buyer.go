package listing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrUserRequired est retourné quand une opération côté acheteur (favori,
// recherche sauvée) est appelée sans identité d'utilisateur.
var ErrUserRequired = errors.New("listing: user_id requis")

// ErrListingRequired est retourné quand un favori est posé sans listing_id.
var ErrListingRequired = errors.New("listing: listing_id requis")

// migrateBuyer crée les tables côté acheteur (favoris, recherches sauvées).
// Idempotent (CREATE TABLE IF NOT EXISTS). Appelé depuis migrate.
func (s *Store) migrateBuyer(ctx context.Context) error {
	// Favoris : clé composite (user, listing) — un favori par paire, idempotent.
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listing_favorites (
		user_id     TEXT NOT NULL,
		listing_id  TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		PRIMARY KEY (user_id, listing_id)
	)`)
	if err != nil {
		return fmt.Errorf("create listing_favorites: %w", err)
	}

	// Recherches sauvées : filtre sérialisé en JSON ; matching évalué en Go par
	// MatchingSavedSearches (pas de prédicat SQL stocké).
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS listing_saved_searches (
		id          TEXT NOT NULL PRIMARY KEY,
		user_id     TEXT NOT NULL,
		name        TEXT NOT NULL DEFAULT '',
		filter      TEXT NOT NULL DEFAULT '{}',
		created_at  TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create listing_saved_searches: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_saved_searches_user ON listing_saved_searches(user_id)`); err != nil {
		return fmt.Errorf("index saved_searches user: %w", err)
	}
	return nil
}

// AddFavorite marque un listing comme favori pour l'utilisateur. Idempotent :
// un second Add sur la même paire ne duplique pas (INSERT OR IGNORE) et ne
// rafraîchit pas created_at.
func (s *Store) AddFavorite(ctx context.Context, userID, listingID string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	if strings.TrimSpace(listingID) == "" {
		return ErrListingRequired
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO listing_favorites (user_id, listing_id, created_at)
		VALUES (?, ?, ?)`,
		userID, listingID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("listing.AddFavorite: %w", err)
	}
	return nil
}

// RemoveFavorite retire un favori. Idempotent : retirer un favori absent n'est
// pas une erreur.
func (s *Store) RemoveFavorite(ctx context.Context, userID, listingID string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	if strings.TrimSpace(listingID) == "" {
		return ErrListingRequired
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM listing_favorites WHERE user_id = ? AND listing_id = ?`,
		userID, listingID)
	if err != nil {
		return fmt.Errorf("listing.RemoveFavorite: %w", err)
	}
	return nil
}

// ListFavorites retourne les listings favoris de l'utilisateur, plus récents en
// premier (date d'ajout du favori). Ne renvoie que les favoris de cet utilisateur
// dont le listing existe encore dans ce silo.
func (s *Store) ListFavorites(ctx context.Context, userID string) ([]Listing, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrUserRequired
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.silo, l.owner_id, l.title, l.body, l.price_cents, l.status, l.attrs, l.medias, l.created_at, l.updated_at
		FROM listing_favorites f
		JOIN listings l ON l.id = f.listing_id
		WHERE f.user_id = ? AND l.silo = ?
		ORDER BY f.created_at DESC`,
		userID, s.silo.ID)
	if err != nil {
		return nil, fmt.Errorf("listing.ListFavorites: %w", err)
	}
	defer rows.Close()
	return scanListings(rows)
}

// SavedSearch est une recherche mémorisée par un utilisateur — un Filter nommé.
// C'est la brique sur laquelle un worker d'alerte se branche via
// MatchingSavedSearches ; la livraison (email) est hors du cœur.
type SavedSearch struct {
	ID        string
	UserID    string
	Name      string
	Filter    Filter
	CreatedAt time.Time
}

// SaveSearch persiste un Filter nommé pour l'utilisateur et retourne la
// SavedSearch créée (id alloué). Le Filter est sérialisé tel quel ; les champs
// de pagination n'ont pas de sens pour le matching mais sont conservés inertes.
func (s *Store) SaveSearch(ctx context.Context, userID, name string, f Filter) (*SavedSearch, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrUserRequired
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("listing.SaveSearch marshal filter: %w", err)
	}
	ss := &SavedSearch{
		ID:        uid.New(),
		UserID:    userID,
		Name:      name,
		Filter:    f,
		CreatedAt: time.Now().UTC(),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO listing_saved_searches (id, user_id, name, filter, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		ss.ID, ss.UserID, ss.Name, string(raw), ss.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("listing.SaveSearch insert: %w", err)
	}
	return ss, nil
}

// ListSavedSearches retourne les recherches sauvées de l'utilisateur, plus
// récentes en premier. Ne renvoie que les recherches de cet utilisateur.
func (s *Store) ListSavedSearches(ctx context.Context, userID string) ([]SavedSearch, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrUserRequired
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, filter, created_at
		FROM listing_saved_searches
		WHERE user_id = ?
		ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("listing.ListSavedSearches: %w", err)
	}
	defer rows.Close()
	return scanSavedSearches(rows)
}

// MatchingSavedSearches retourne les recherches sauvées (tous utilisateurs)
// dont le Filter correspond au listing donné. Primitive de matching sur laquelle
// un worker d'alerte se branchera ; aucune livraison n'est faite ici.
//
// Le matching est évalué en Go (matchFilter) pour rester correct et explicite :
// un listing hors critère n'est jamais renvoyé.
func (s *Store) MatchingSavedSearches(ctx context.Context, l *Listing) ([]SavedSearch, error) {
	if l == nil {
		return nil, errors.New("listing.MatchingSavedSearches: listing nil")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, filter, created_at
		FROM listing_saved_searches
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing.MatchingSavedSearches: %w", err)
	}
	defer rows.Close()
	all, err := scanSavedSearches(rows)
	if err != nil {
		return nil, err
	}
	var out []SavedSearch
	for _, ss := range all {
		if s.matchFilter(ss.Filter, l) {
			out = append(out, ss)
		}
	}
	return out, nil
}

// matchFilter évalue si un listing satisfait un Filter, en miroir exact des
// clauses WHERE de Search (silo, owner, status, facettes exactes, ranges, texte).
// Tout critère renseigné qui ne colle pas exclut le listing — pas de faux positif.
// Le texte libre est testé par sous-chaîne insensible à la casse sur title+body :
// approximation conservatrice (un terme absent exclut), suffisante pour la
// primitive d'alerte ; le ranking FTS reste l'affaire de Search.
func (s *Store) matchFilter(f Filter, l *Listing) bool {
	if l.Silo != s.silo.ID {
		return false
	}
	if f.Owner != "" && l.OwnerID != f.Owner {
		return false
	}
	if f.Status != "" && l.Status != f.Status {
		return false
	}
	for name, want := range f.Facets {
		if want == "" {
			continue
		}
		got, ok := l.Attrs[name]
		if !ok || attrAsString(got) != want {
			return false
		}
	}
	for attr, rf := range f.Ranges {
		var val float64
		if attr == "price" {
			val = float64(l.PriceCents)
		} else {
			got, ok := l.Attrs[attr]
			if !ok {
				return false
			}
			fv, ok := attrAsFloat(got)
			if !ok {
				return false
			}
			val = fv
		}
		if rf.Min != nil && val < *rf.Min {
			return false
		}
		if rf.Max != nil && val > *rf.Max {
			return false
		}
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		hay := strings.ToLower(l.Title + " " + l.Body)
		for _, term := range strings.Fields(strings.ToLower(t)) {
			if !strings.Contains(hay, term) {
				return false
			}
		}
	}
	return true
}

// attrAsString rend une valeur d'attribut JSON comparable à une facette texte.
func attrAsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", x)
	}
}

// attrAsFloat extrait une valeur numérique d'un attribut JSON (les nombres
// décodés depuis JSON sont des float64). Retourne false si non numérique.
func attrAsFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func scanSavedSearches(rows *sql.Rows) ([]SavedSearch, error) {
	var out []SavedSearch
	for rows.Next() {
		var ss SavedSearch
		var filterRaw, createdAt string
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.Name, &filterRaw, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(filterRaw), &ss.Filter); err != nil {
			return nil, fmt.Errorf("scanSavedSearches unmarshal filter: %w", err)
		}
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			ss.CreatedAt = t
		}
		out = append(out, ss)
	}
	if out == nil {
		return []SavedSearch{}, rows.Err()
	}
	return out, rows.Err()
}
