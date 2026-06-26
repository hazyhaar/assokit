package listing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// allowedTransitions définit les transitions de statut autorisées.
// draft → published → sold/archived. Aucun retour depuis un état terminal
// (sold/archived) : un listing vendu ou archivé ne se republie pas.
var allowedTransitions = map[Status]map[Status]bool{
	StatusDraft: {
		StatusPublished: true,
		StatusArchived:  true,
	},
	StatusPublished: {
		StatusSold:     true,
		StatusArchived: true,
		StatusDraft:    true, // dépublication → retour brouillon
	},
	StatusSold:     {},
	StatusArchived: {},
}

// Get charge un listing par id. Retourne ErrNotFound si absent.
func (s *Store) Get(ctx context.Context, id string) (*Listing, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.silo, l.owner_id, l.title, l.body, l.price_cents, l.status, l.attrs, l.medias, l.created_at, l.updated_at
		FROM listings l
		WHERE l.id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("listing.Get: %w", err)
	}
	defer rows.Close()
	out, err := scanListings(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// Patch porte les champs éditables d'un listing. Un pointeur nil = champ inchangé.
type Patch struct {
	Title      *string
	Body       *string
	PriceCents *int64
	Attrs      map[string]any // non-nil = remplace intégralement les attrs
	Medias     []string       // non-nil = remplace la liste ordonnée de médias
}

// Update édite un listing existant. OWNER-SCOPED : callerID doit être l'owner,
// sinon ErrNotOwner. Bump UpdatedAt. Retourne ErrNotFound si le listing n'existe pas.
func (s *Store) Update(ctx context.Context, callerID, id string, p Patch) (*Listing, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.OwnerID != callerID {
		return nil, ErrNotOwner
	}

	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Body != nil {
		cur.Body = *p.Body
	}
	if p.PriceCents != nil {
		cur.PriceCents = *p.PriceCents
	}
	if p.Attrs != nil {
		cur.Attrs = p.Attrs
	}
	if p.Medias != nil {
		cur.Medias = p.Medias
	}

	attrsJSON, err := json.Marshal(cur.Attrs)
	if err != nil {
		return nil, fmt.Errorf("listing.Update marshal attrs: %w", err)
	}
	mediasJSON, err := marshalMedias(cur.Medias)
	if err != nil {
		return nil, fmt.Errorf("listing.Update marshal medias: %w", err)
	}
	now := time.Now().UTC()
	cur.UpdatedAt = now

	_, err = s.db.ExecContext(ctx, `
		UPDATE listings SET title=?, body=?, price_cents=?, attrs=?, medias=?, updated_at=?
		WHERE id=?`,
		cur.Title, cur.Body, cur.PriceCents, string(attrsJSON), mediasJSON,
		now.Format(time.RFC3339), id,
	)
	if err != nil {
		return nil, fmt.Errorf("listing.Update: %w", err)
	}
	if err := s.refreshFacetCounts(ctx); err != nil {
		return nil, err
	}
	return cur, nil
}

// SetStatus applique une transition de cycle de vie VALIDÉE. OWNER-SCOPED.
// Rejette toute transition non déclarée dans allowedTransitions avec
// ErrIllegalTransition. Bump UpdatedAt.
func (s *Store) SetStatus(ctx context.Context, callerID, id string, to Status) (*Listing, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.OwnerID != callerID {
		return nil, ErrNotOwner
	}
	if cur.Status == to {
		return cur, nil
	}
	if !allowedTransitions[cur.Status][to] {
		return nil, fmt.Errorf("%w: %s → %s", ErrIllegalTransition, cur.Status, to)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`UPDATE listings SET status=?, updated_at=? WHERE id=?`,
		string(to), now.Format(time.RFC3339), id,
	)
	if err != nil {
		return nil, fmt.Errorf("listing.SetStatus: %w", err)
	}
	cur.Status = to
	cur.UpdatedAt = now
	return cur, nil
}
