// CLAUDE:SUMMARY Messagerie privée membre↔membre : Store Send/Inbox/Thread/MarkRead, conversations implicites par paire, md→HTML. Import-clean (publiable).
package messages

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

// ErrEmptyBody est retourné quand le corps du message est vide.
var ErrEmptyBody = errors.New("messages: corps vide")

// ErrRecipientInvalid est retourné quand le destinataire n'existe pas / est inactif.
var ErrRecipientInvalid = errors.New("messages: destinataire introuvable ou inactif")

// ErrSelf est retourné quand un membre tente de s'écrire à lui-même.
var ErrSelf = errors.New("messages: impossible de s'écrire à soi-même")

// Message est un message privé.
type Message struct {
	ID          string
	SenderID    string
	RecipientID string
	BodyMD      string
	BodyHTML    string
	CreatedAt   time.Time
	ReadAt      sql.NullTime
}

// Conversation résume un fil avec un correspondant (vue inbox).
type Conversation struct {
	WithUserID string
	WithName   string
	WithEmail  string
	LastBody   string
	LastAt     time.Time
	Unread     int
}

// Store est le dépôt de messages.
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

// Send crée un message de senderID vers recipientID. Valide que le destinataire
// existe et est actif, et qu'il diffère de l'expéditeur. Retourne l'ID créé.
func (s *Store) Send(ctx context.Context, senderID, recipientID, bodyMD string) (string, error) {
	bodyMD = strings.TrimSpace(bodyMD)
	if bodyMD == "" {
		return "", ErrEmptyBody
	}
	if senderID == recipientID {
		return "", ErrSelf
	}
	var active int
	err := s.DB.QueryRowContext(ctx, `SELECT is_active FROM users WHERE id=?`, recipientID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return "", ErrRecipientInvalid
	}
	if err != nil {
		return "", fmt.Errorf("messages.Send check recipient: %w", err)
	}

	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO messages(id, sender_id, recipient_id, body_md, body_html, created_at)
		 VALUES(?,?,?,?,?,?)`,
		id, senderID, recipientID, bodyMD, renderMD(bodyMD), now,
	)
	if err != nil {
		return "", fmt.Errorf("messages.Send: %w", err)
	}
	return id, nil
}

// Inbox retourne les conversations de userID (dernier message par correspondant
// + nombre de non-lus), les plus récentes d'abord.
func (s *Store) Inbox(ctx context.Context, userID string) ([]Conversation, error) {
	rows, err := s.DB.QueryContext(ctx, `
		WITH conv AS (
			SELECT
				CASE WHEN sender_id=? THEN recipient_id ELSE sender_id END AS other_id,
				body_md, created_at, read_at, recipient_id
			FROM messages
			WHERE sender_id=? OR recipient_id=?
		)
		SELECT c.other_id, u.display_name, u.email,
			(SELECT body_md FROM conv c2 WHERE c2.other_id=c.other_id ORDER BY created_at DESC LIMIT 1) AS last_body,
			MAX(c.created_at) AS last_at,
			SUM(CASE WHEN c.recipient_id=? AND c.read_at IS NULL THEN 1 ELSE 0 END) AS unread
		FROM conv c JOIN users u ON u.id=c.other_id
		GROUP BY c.other_id
		ORDER BY last_at DESC
	`, userID, userID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("messages.Inbox: %w", err)
	}
	defer rows.Close()

	var result []Conversation
	for rows.Next() {
		var c Conversation
		var lastAt string
		if err := rows.Scan(&c.WithUserID, &c.WithName, &c.WithEmail, &c.LastBody, &lastAt, &c.Unread); err != nil {
			return nil, err
		}
		c.LastAt, _ = time.Parse(tsLayout, lastAt)
		result = append(result, c)
	}
	return result, rows.Err()
}

// Thread retourne les messages échangés entre userID et withUserID, du plus
// ancien au plus récent (limité à limit, 0 = défaut 200).
func (s *Store) Thread(ctx context.Context, userID, withUserID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, sender_id, recipient_id, body_md, body_html, created_at, read_at
		FROM messages
		WHERE (sender_id=? AND recipient_id=?) OR (sender_id=? AND recipient_id=?)
		ORDER BY created_at ASC
		LIMIT ?
	`, userID, withUserID, withUserID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("messages.Thread: %w", err)
	}
	defer rows.Close()

	var result []Message
	for rows.Next() {
		var m Message
		var createdAt string
		var readAt sql.NullString
		if err := rows.Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.BodyMD, &m.BodyHTML, &createdAt, &readAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(tsLayout, createdAt)
		if readAt.Valid {
			t, _ := time.Parse(tsLayout, readAt.String)
			m.ReadAt = sql.NullTime{Time: t, Valid: true}
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// MarkRead marque comme lus les messages reçus par userID en provenance de
// withUserID. Retourne le nombre de messages marqués.
func (s *Store) MarkRead(ctx context.Context, userID, withUserID string) (int64, error) {
	now := time.Now().UTC().Format(tsLayout)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE messages SET read_at=? WHERE recipient_id=? AND sender_id=? AND read_at IS NULL`,
		now, userID, withUserID,
	)
	if err != nil {
		return 0, fmt.Errorf("messages.MarkRead: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
