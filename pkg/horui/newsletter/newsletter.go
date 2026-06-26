// CLAUDE:SUMMARY Newsletter/diffusion : Store Create/List/Get/MarkSent + ActiveMemberEmails/ActiveMembershipEmails (audience), md→HTML goldmark. N'envoie pas (le Store ignore le mailer). Import-clean (publiable).
package newsletter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// ErrEmptySubject est retourné quand le sujet de la diffusion est vide.
var ErrEmptySubject = errors.New("newsletter: sujet vide")

// ErrEmptyBody est retourné quand le corps de la diffusion est vide.
var ErrEmptyBody = errors.New("newsletter: corps vide")

// ErrNotFound est retourné quand la diffusion demandée n'existe pas.
var ErrNotFound = errors.New("newsletter: introuvable")

// Newsletter est une diffusion (brouillon tant que SentAt est nul).
type Newsletter struct {
	ID              string
	Subject         string
	BodyMD          string
	BodyHTML        string
	CreatedBy       string
	SentAt          sql.NullTime
	RecipientsCount int
	CreatedAt       time.Time
}

// Store est le dépôt de diffusions.
type Store struct {
	DB *sql.DB
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

func renderMD(src string) string {
	var b strings.Builder
	if err := md.Convert([]byte(src), &b); err != nil {
		return src
	}
	return b.String()
}

const tsLayout = "2006-01-02 15:04:05"

// Create crée une diffusion en brouillon. Rend body_html depuis le markdown,
// génère l'id. Retourne l'id créé.
func (s *Store) Create(ctx context.Context, n Newsletter) (string, error) {
	n.Subject = strings.TrimSpace(n.Subject)
	n.BodyMD = strings.TrimSpace(n.BodyMD)
	if n.Subject == "" {
		return "", ErrEmptySubject
	}
	if n.BodyMD == "" {
		return "", ErrEmptyBody
	}
	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO newsletters(id, subject, body_md, body_html, created_by, created_at)
		 VALUES(?,?,?,?,?,?)`,
		id, n.Subject, n.BodyMD, renderMD(n.BodyMD), n.CreatedBy, now,
	)
	if err != nil {
		return "", fmt.Errorf("newsletter.Create: %w", err)
	}
	return id, nil
}

// List retourne les diffusions, les plus récentes d'abord.
func (s *Store) List(ctx context.Context) ([]Newsletter, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, subject, body_md, body_html, created_by, sent_at, recipients_count, created_at
		FROM newsletters
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("newsletter.List: %w", err)
	}
	defer rows.Close()

	var result []Newsletter
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// Get retourne une diffusion par id, ErrNotFound si absente.
func (s *Store) Get(ctx context.Context, id string) (Newsletter, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, subject, body_md, body_html, created_by, sent_at, recipients_count, created_at
		FROM newsletters WHERE id=?
	`, id)
	n, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Newsletter{}, ErrNotFound
	}
	if err != nil {
		return Newsletter{}, fmt.Errorf("newsletter.Get: %w", err)
	}
	return n, nil
}

// MarkSent pose sent_at (maintenant) et recipients_count sur la diffusion.
func (s *Store) MarkSent(ctx context.Context, id string, count int) error {
	now := time.Now().UTC().Format(tsLayout)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE newsletters SET sent_at=?, recipients_count=? WHERE id=?`,
		now, count, id,
	)
	if err != nil {
		return fmt.Errorf("newsletter.MarkSent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveMemberEmails retourne les adresses des membres actifs (is_active=1),
// destinataires d'une diffusion v1.
func (s *Store) ActiveMemberEmails(ctx context.Context) ([]string, error) {
	return s.scanEmails(ctx, `SELECT email FROM users WHERE is_active=1`)
}

// ActiveMembershipEmails retourne les emails des comptes actifs possédant une
// adhésion au statut 'active' (audience « adhérents à jour »). Distinct : un
// membre avec plusieurs adhésions actives n'est compté qu'une fois.
func (s *Store) ActiveMembershipEmails(ctx context.Context) ([]string, error) {
	return s.scanEmails(ctx, `
		SELECT DISTINCT u.email FROM users u
		JOIN memberships m ON m.user_id = u.id
		WHERE u.is_active = 1 AND m.status = 'active'`)
}

func (s *Store) scanEmails(ctx context.Context, query string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("newsletter.scanEmails: %w", err)
	}
	defer rows.Close()

	var emails []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}

// scanner abstrait QueryRow et Rows pour un Scan partagé.
type scanner interface {
	Scan(dest ...any) error
}

func scan(sc scanner) (Newsletter, error) {
	var n Newsletter
	var createdAt string
	var createdBy sql.NullString
	var sentAt sql.NullString
	if err := sc.Scan(&n.ID, &n.Subject, &n.BodyMD, &n.BodyHTML, &createdBy, &sentAt, &n.RecipientsCount, &createdAt); err != nil {
		return Newsletter{}, err
	}
	n.CreatedBy = createdBy.String
	n.CreatedAt, _ = time.Parse(tsLayout, createdAt)
	if sentAt.Valid {
		t, _ := time.Parse(tsLayout, sentAt.String)
		n.SentAt = sql.NullTime{Time: t, Valid: true}
	}
	return n, nil
}
