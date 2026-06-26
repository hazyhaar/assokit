package listing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrSelfInquiry est retourné quand un owner tente de s'enquérir de son propre listing.
var ErrSelfInquiry = errors.New("listing: l'owner ne peut pas s'enquérir de son propre listing")

// Messenger est l'abstraction CONSOMMATEUR de la messagerie, définie ici (côté
// consommateur) plutôt qu'importée en dur. pkg/messaging.Store la satisfait :
// Send(ctx, senderID, recipientID, bodyMD) (string, error). L'inquiry réutilise
// la messagerie existante au lieu de la réimplémenter.
type Messenger interface {
	Send(ctx context.Context, senderID, recipientID, bodyMD string) (string, error)
}

// Inquiry est le lien acheteur↔owner↔listing matérialisant un contact ouvert.
type Inquiry struct {
	ListingID string
	BuyerID   string
	OwnerID   string
	MessageID string // id du message d'ouverture dans pkg/messaging
	CreatedAt time.Time
}

// OpenInquiry ouvre (ou récupère si déjà ouverte) une conversation entre un
// acheteur et l'OWNER d'un listing, en référençant le listing. Idempotente :
// un 2e appel pour la même paire (listing, acheteur) ne crée pas de nouveau
// message et retourne l'inquiry existante.
//
// La conversation vit dans la messagerie (msgr) ; ici on ne mémorise que le
// lien d'idempotence. bodyMD n'est utilisé que lors de la PREMIÈRE ouverture.
func (s *Store) OpenInquiry(ctx context.Context, msgr Messenger, listingID, buyerID, bodyMD string) (*Inquiry, error) {
	if msgr == nil {
		return nil, errors.New("listing.OpenInquiry: messenger nil")
	}
	if strings.TrimSpace(buyerID) == "" {
		return nil, errors.New("listing.OpenInquiry: buyer_id requis")
	}

	// Idempotence : inquiry déjà ouverte pour cette paire ?
	if existing, err := s.getInquiry(ctx, listingID, buyerID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	l, err := s.Get(ctx, listingID)
	if err != nil {
		return nil, err
	}
	if l.OwnerID == buyerID {
		return nil, ErrSelfInquiry
	}

	body := strings.TrimSpace(bodyMD)
	if body == "" {
		body = fmt.Sprintf("Demande d'information concernant l'annonce « %s ».", l.Title)
	}

	msgID, err := msgr.Send(ctx, buyerID, l.OwnerID, body)
	if err != nil {
		return nil, fmt.Errorf("listing.OpenInquiry send: %w", err)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO listing_inquiries (listing_id, buyer_id, owner_id, message_id, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		listingID, buyerID, l.OwnerID, msgID, now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("listing.OpenInquiry persist: %w", err)
	}

	return &Inquiry{
		ListingID: listingID,
		BuyerID:   buyerID,
		OwnerID:   l.OwnerID,
		MessageID: msgID,
		CreatedAt: now,
	}, nil
}

// getInquiry retourne l'inquiry existante pour (listing, acheteur) ou nil.
func (s *Store) getInquiry(ctx context.Context, listingID, buyerID string) (*Inquiry, error) {
	var inq Inquiry
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT listing_id, buyer_id, owner_id, message_id, created_at
		FROM listing_inquiries WHERE listing_id=? AND buyer_id=?`,
		listingID, buyerID,
	).Scan(&inq.ListingID, &inq.BuyerID, &inq.OwnerID, &inq.MessageID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing.getInquiry: %w", err)
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		inq.CreatedAt = t
	}
	return &inq, nil
}
