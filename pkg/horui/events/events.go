// CLAUDE:SUMMARY Événements / agenda communautaire : Store Create/List/Get/Update/SoftDelete, slug auto, description md→HTML. Liste publique (à venir d'abord) ; création/suppression via perm events.manage. Import-clean (publiable).
package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// ErrNotFound est retourné quand l'événement n'existe pas (ou est supprimé).
var ErrNotFound = errors.New("events: introuvable")

// ErrSlugTaken est retourné quand le slug est déjà utilisé.
var ErrSlugTaken = errors.New("events: slug déjà utilisé")

// ErrEmptyTitle est retourné quand le titre est vide.
var ErrEmptyTitle = errors.New("events: titre vide")

// ErrEmptyStart est retourné quand la date de début est vide.
var ErrEmptyStart = errors.New("events: date de début requise")

// Event est un événement de l'agenda communautaire.
type Event struct {
	ID        string
	Slug      string
	Title     string
	DescMD    string
	DescHTML  string
	Location  string
	StartsAt  string         // ISO 8601, ex. "2026-06-15T18:30"
	EndsAt    sql.NullString // optionnel
	CreatedBy sql.NullString
	CreatedAt time.Time
}

// Store est le dépôt d'événements.
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

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// slugify produit un slug snake/dash depuis un titre libre.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugInvalid.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// Create insère un événement. Génère le slug depuis le titre si vide, rend la
// description HTML, et retourne l'ID créé. Erreurs : ErrEmptyTitle,
// ErrEmptyStart, ErrSlugTaken.
func (s *Store) Create(ctx context.Context, e Event) (string, error) {
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return "", ErrEmptyTitle
	}
	e.StartsAt = strings.TrimSpace(e.StartsAt)
	if e.StartsAt == "" {
		return "", ErrEmptyStart
	}
	slug := strings.TrimSpace(e.Slug)
	if slug == "" {
		slug = slugify(e.Title)
	}
	if slug == "" {
		slug = uid.New()
	}

	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO events(id, slug, title, description_md, description_html, location, starts_at, ends_at, created_by, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, slug, e.Title, e.DescMD, renderMD(e.DescMD), e.Location, e.StartsAt, e.EndsAt, e.CreatedBy, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", ErrSlugTaken
		}
		return "", fmt.Errorf("events.Create: %w", err)
	}
	return id, nil
}

const selectCols = `id, slug, title, description_md, description_html, location, starts_at, ends_at, created_by, created_at`

func scanEvent(sc interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var createdAt string
	if err := sc.Scan(&e.ID, &e.Slug, &e.Title, &e.DescMD, &e.DescHTML, &e.Location, &e.StartsAt, &e.EndsAt, &e.CreatedBy, &createdAt); err != nil {
		return Event{}, err
	}
	e.CreatedAt, _ = time.Parse(tsLayout, createdAt)
	return e, nil
}

// List retourne les événements non supprimés, à venir d'abord (les plus
// proches en tête), puis les passés. Ordre par starts_at croissant.
func (s *Store) List(ctx context.Context) ([]Event, error) {
	now := time.Now().UTC().Format(tsLayout)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+selectCols+` FROM events
		 WHERE deleted_at IS NULL
		 ORDER BY (starts_at < ?) ASC, starts_at ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("events.List: %w", err)
	}
	defer rows.Close()

	var result []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Get retourne un événement non supprimé par son id OU son slug.
func (s *Store) Get(ctx context.Context, idOrSlug string) (Event, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+selectCols+` FROM events
		 WHERE (id=? OR slug=?) AND deleted_at IS NULL`, idOrSlug, idOrSlug)
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("events.Get: %w", err)
	}
	return e, nil
}

// Update modifie un événement existant (titre, description, lieu, horaires).
// Le slug n'est pas modifié. Retourne ErrNotFound si l'id est inconnu/supprimé,
// ErrEmptyTitle / ErrEmptyStart sur validation.
func (s *Store) Update(ctx context.Context, e Event) error {
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return ErrEmptyTitle
	}
	e.StartsAt = strings.TrimSpace(e.StartsAt)
	if e.StartsAt == "" {
		return ErrEmptyStart
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE events
		 SET title=?, description_md=?, description_html=?, location=?, starts_at=?, ends_at=?
		 WHERE id=? AND deleted_at IS NULL`,
		e.Title, e.DescMD, renderMD(e.DescMD), e.Location, e.StartsAt, e.EndsAt, e.ID,
	)
	if err != nil {
		return fmt.Errorf("events.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete marque un événement comme supprimé (deleted_at). Retourne
// ErrNotFound si l'id est inconnu ou déjà supprimé.
func (s *Store) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(tsLayout)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE events SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, now, id,
	)
	if err != nil {
		return fmt.Errorf("events.SoftDelete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
