// CLAUDE:SUMMARY Salons (chat de groupe) : Create/List/Get/Join/Leave/SetPassword/ToggleVisio/PostMessage/Messages/Archive. Slug unique auto-généré. Mot de passe bcrypt cost=12 optionnel.
package salon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/markup"
	"github.com/hazyhaar/assokit/pkg/uid"
	"golang.org/x/crypto/bcrypt"
)

// ErrNotFound est retourné quand le salon demandé n'existe pas.
var ErrNotFound = errors.New("salon: introuvable")

// ErrWrongPassword est retourné quand le mot de passe fourni est incorrect.
var ErrWrongPassword = errors.New("salon: mot de passe incorrect")

// ErrPasswordRequired est retourné quand le salon est protégé et qu'aucun mot de passe n'est fourni.
var ErrPasswordRequired = errors.New("salon: mot de passe requis")

// ErrAlreadyMember est retourné quand l'utilisateur est déjà membre du salon.
var ErrAlreadyMember = errors.New("salon: déjà membre")

// ErrArchived est retourné quand une opération est tentée sur un salon archivé.
var ErrArchived = errors.New("salon: salon archivé")

const tsLayout = "2006-01-02 15:04:05"

// Salon représente un salon de discussion de groupe.
type Salon struct {
	ID                   string
	Name                 string
	Slug                 string
	CreatorID            string
	PasswordHash         sql.NullString
	VisioEnabled         bool
	TranscriptionConsent bool
	CreatedAt            time.Time
	ArchivedAt           sql.NullTime
}

// Message est un message posté dans un salon.
type Message struct {
	ID        string
	SalonID   string
	AuthorID  string
	Body      string
	BodyHTML  string
	CreatedAt time.Time
}

// Store est le dépôt des salons.
type Store struct {
	DB *sql.DB
}

var nonAlphanumRE = regexp.MustCompile(`[^a-z0-9]+`)

// slugify génère un slug à partir d'un nom : minuscules, tirets, sans caractères spéciaux.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = nonAlphanumRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "salon"
	}
	return s
}

// uniqueSlug génère un slug unique en ajoutant un suffixe si nécessaire.
func uniqueSlug(ctx context.Context, db *sql.DB, base string) (string, error) {
	slug := base
	for i := 2; ; i++ {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM salon WHERE slug=?`, slug).Scan(&count); err != nil {
			return "", fmt.Errorf("salon.uniqueSlug: %w", err)
		}
		if count == 0 {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

// Create crée un salon et ajoute le créateur comme membre role='owner'.
// password peut être vide (salon sans mot de passe).
func (s *Store) Create(ctx context.Context, name, ownerID, password string, visioEnabled bool) (*Salon, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("salon: nom vide")
	}

	slug, err := uniqueSlug(ctx, s.DB, slugify(name))
	if err != nil {
		return nil, err
	}

	var hashStr sql.NullString
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return nil, fmt.Errorf("salon.Create bcrypt: %w", err)
		}
		hashStr = sql.NullString{String: string(h), Valid: true}
	}

	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	visio := 0
	if visioEnabled {
		visio = 1
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("salon.Create begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO salon(id, name, slug, creator_id, password_hash, visio_enabled, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id, name, slug, ownerID, hashStr, visio, now,
	)
	if err != nil {
		return nil, fmt.Errorf("salon.Create insert salon: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO salon_member(salon_id, user_id, role, joined_at) VALUES(?,?,?,?)`,
		id, ownerID, "owner", now,
	)
	if err != nil {
		return nil, fmt.Errorf("salon.Create insert owner member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("salon.Create commit: %w", err)
	}

	return &Salon{
		ID:           id,
		Name:         name,
		Slug:         slug,
		CreatorID:    ownerID,
		PasswordHash: hashStr,
		VisioEnabled: visioEnabled,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// List retourne les salons dont userID est membre (non archivés en premier, par date de création desc).
func (s *Store) List(ctx context.Context, userID string) ([]Salon, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT s.id, s.name, s.slug, s.creator_id, s.password_hash, s.visio_enabled, s.transcription_consent, s.created_at, s.archived_at
		FROM salon s
		JOIN salon_member sm ON sm.salon_id = s.id
		WHERE sm.user_id = ?
		ORDER BY s.archived_at IS NOT NULL ASC, s.created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("salon.List: %w", err)
	}
	defer rows.Close()
	return scanSalons(rows)
}

// Get retourne un salon par son slug.
func (s *Store) Get(ctx context.Context, slug string) (*Salon, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, slug, creator_id, password_hash, visio_enabled, transcription_consent, created_at, archived_at FROM salon WHERE slug=?`, slug)
	sl, err := scanSalon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sl, err
}

// Join ajoute userID comme membre du salon identifié par slug.
// Si le salon est protégé par un mot de passe, password doit correspondre.
func (s *Store) Join(ctx context.Context, slug, userID, password string) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	if sl.ArchivedAt.Valid {
		return ErrArchived
	}
	if sl.PasswordHash.Valid {
		if password == "" {
			return ErrPasswordRequired
		}
		if err := bcrypt.CompareHashAndPassword([]byte(sl.PasswordHash.String), []byte(password)); err != nil {
			return ErrWrongPassword
		}
	}

	now := time.Now().UTC().Format(tsLayout)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO salon_member(salon_id, user_id, role, joined_at) VALUES(?,?,?,?)`,
		sl.ID, userID, "member", now,
	)
	if err != nil {
		// Contrainte UNIQUE (PRIMARY KEY) violée = déjà membre.
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrAlreadyMember
		}
		return fmt.Errorf("salon.Join: %w", err)
	}
	return nil
}

// Leave retire userID du salon.
func (s *Store) Leave(ctx context.Context, slug, userID string) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`DELETE FROM salon_member WHERE salon_id=? AND user_id=?`, sl.ID, userID)
	if err != nil {
		return fmt.Errorf("salon.Leave: %w", err)
	}
	return nil
}

// SetPassword change ou supprime le mot de passe du salon.
// password vide => suppression (NULL). Sinon bcrypt cost=12.
func (s *Store) SetPassword(ctx context.Context, slug, password string) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	var hashStr sql.NullString
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return fmt.Errorf("salon.SetPassword bcrypt: %w", err)
		}
		hashStr = sql.NullString{String: string(h), Valid: true}
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE salon SET password_hash=? WHERE id=?`, hashStr, sl.ID)
	if err != nil {
		return fmt.Errorf("salon.SetPassword: %w", err)
	}
	return nil
}

// ToggleVisio active ou désactive l'option visio du salon.
func (s *Store) ToggleVisio(ctx context.Context, slug string, on bool) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	visio := 0
	if on {
		visio = 1
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE salon SET visio_enabled=? WHERE id=?`, visio, sl.ID)
	if err != nil {
		return fmt.Errorf("salon.ToggleVisio: %w", err)
	}
	return nil
}

// ToggleTranscriptionConsent active ou désactive le consentement RGPD à la transcription.
// Sans ce consentement (valeur false), le bot transcripteur ne démarre pas (cf. ShouldTranscribe).
func (s *Store) ToggleTranscriptionConsent(ctx context.Context, slug string, on bool) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	consent := 0
	if on {
		consent = 1
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE salon SET transcription_consent=? WHERE id=?`, consent, sl.ID)
	if err != nil {
		return fmt.Errorf("salon.ToggleTranscriptionConsent: %w", err)
	}
	return nil
}

// ShouldTranscribe retourne true uniquement si la visio ET le consentement RGPD sont tous les deux actifs.
// C'est le garde-fou que le bot transcripteur (T3) doit appeler avant de démarrer.
func (s *Store) ShouldTranscribe(ctx context.Context, slug string) (bool, error) {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return false, err
	}
	return sl.VisioEnabled && sl.TranscriptionConsent, nil
}

// PostMessage publie un message dans le salon. body_html est généré via internal/markup.
func (s *Store) PostMessage(ctx context.Context, slug, authorID, body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", errors.New("salon: corps de message vide")
	}
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return "", err
	}
	if sl.ArchivedAt.Valid {
		return "", ErrArchived
	}

	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	bodyHTML := markup.Render(body)

	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO salon_message(id, salon_id, author_id, body, body_html, created_at) VALUES(?,?,?,?,?,?)`,
		id, sl.ID, authorID, body, bodyHTML, now,
	)
	if err != nil {
		return "", fmt.Errorf("salon.PostMessage: %w", err)
	}
	return id, nil
}

// Messages retourne les messages d'un salon, du plus récent au plus ancien (limit 0 = 100).
func (s *Store) Messages(ctx context.Context, slug string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, salon_id, author_id, body, body_html, created_at
		FROM salon_message
		WHERE salon_id=?
		ORDER BY created_at DESC
		LIMIT ?
	`, sl.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("salon.Messages: %w", err)
	}
	defer rows.Close()

	var result []Message
	for rows.Next() {
		var m Message
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SalonID, &m.AuthorID, &m.Body, &m.BodyHTML, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(tsLayout, createdAt)
		result = append(result, m)
	}
	return result, rows.Err()
}

// Archive marque le salon comme archivé (archived_at = now).
func (s *Store) Archive(ctx context.Context, slug string) error {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(tsLayout)
	_, err = s.DB.ExecContext(ctx,
		`UPDATE salon SET archived_at=? WHERE id=?`, now, sl.ID)
	if err != nil {
		return fmt.Errorf("salon.Archive: %w", err)
	}
	return nil
}

// IsOwner retourne true si userID est membre du salon identifié par slug avec le rôle 'owner'.
func (s *Store) IsOwner(ctx context.Context, slug, userID string) (bool, error) {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return false, err
	}
	var count int
	err = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM salon_member WHERE salon_id=? AND user_id=? AND role='owner'`, sl.ID, userID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("salon.IsOwner: %w", err)
	}
	return count > 0, nil
}

// IsMember retourne true si userID est membre actif du salon identifié par slug.
func (s *Store) IsMember(ctx context.Context, slug, userID string) (bool, error) {
	sl, err := s.Get(ctx, slug)
	if err != nil {
		return false, err
	}
	var count int
	err = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM salon_member WHERE salon_id=? AND user_id=?`, sl.ID, userID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("salon.IsMember: %w", err)
	}
	return count > 0, nil
}

// scanSalon lit un salon depuis une *sql.Row.
func scanSalon(row *sql.Row) (*Salon, error) {
	var sl Salon
	var createdAt string
	var archivedAt sql.NullString
	var visio, consent int
	err := row.Scan(&sl.ID, &sl.Name, &sl.Slug, &sl.CreatorID,
		&sl.PasswordHash, &visio, &consent, &createdAt, &archivedAt)
	if err != nil {
		return nil, err
	}
	sl.VisioEnabled = visio != 0
	sl.TranscriptionConsent = consent != 0
	sl.CreatedAt, _ = time.Parse(tsLayout, createdAt)
	if archivedAt.Valid {
		t, _ := time.Parse(tsLayout, archivedAt.String)
		sl.ArchivedAt = sql.NullTime{Time: t, Valid: true}
	}
	return &sl, nil
}

// scanSalons lit une liste de salons depuis *sql.Rows.
func scanSalons(rows *sql.Rows) ([]Salon, error) {
	var result []Salon
	for rows.Next() {
		var sl Salon
		var createdAt string
		var archivedAt sql.NullString
		var visio, consent int
		if err := rows.Scan(&sl.ID, &sl.Name, &sl.Slug, &sl.CreatorID,
			&sl.PasswordHash, &visio, &consent, &createdAt, &archivedAt); err != nil {
			return nil, err
		}
		sl.VisioEnabled = visio != 0
		sl.TranscriptionConsent = consent != 0
		sl.CreatedAt, _ = time.Parse(tsLayout, createdAt)
		if archivedAt.Valid {
			t, _ := time.Parse(tsLayout, archivedAt.String)
			sl.ArchivedAt = sql.NullTime{Time: t, Valid: true}
		}
		result = append(result, sl)
	}
	return result, rows.Err()
}
