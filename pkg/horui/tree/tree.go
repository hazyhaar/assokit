// CLAUDE:SUMMARY Arbre récursif de nœuds : CRUD, slug global, soft-delete cascade, depth dénormalisée, anti-cycle, markdown→HTML.
package tree

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// ErrSlugTaken est retourné quand le slug demandé existe déjà.
var ErrSlugTaken = errors.New("tree: slug déjà pris")

// ErrCycle est retourné quand le reparentage créerait un cycle.
var ErrCycle = errors.New("tree: reparentage créerait un cycle")

// ErrNotFound est retourné quand le nœud n'existe pas.
var ErrNotFound = errors.New("tree: nœud introuvable")

// Node représente un nœud de l'arbre éditorial.
type Node struct {
	ID           string
	ParentID     sql.NullString
	Slug         string
	Type         string
	Title        string
	BodyMD       string
	BodyHTML     string
	Visibility   string
	AuthorID     sql.NullString
	DisplayOrder int
	Depth        int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    sql.NullTime
}

// Store est le dépôt de nœuds.
type Store struct {
	DB *sql.DB
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// renderMD convertit markdown en HTML via goldmark.
func renderMD(src string) string {
	var b strings.Builder
	if err := md.Convert([]byte(src), &b); err != nil {
		return src
	}
	return b.String()
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// slugify génère un slug URL-safe depuis un titre.
func slugify(title string) string {
	s := strings.ToLower(title)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	result := slugRe.ReplaceAllString(b.String(), "-")
	result = strings.Trim(result, "-")
	if result == "" {
		result = uuid.New().String()[:8]
	}
	return result
}

// Create crée un nouveau nœud. Si n.Slug est vide, le génère depuis n.Title.
// Retourne ErrSlugTaken si le slug existe déjà.
func (s *Store) Create(ctx context.Context, n Node) (string, error) {
	if n.Slug == "" {
		n.Slug = slugify(n.Title)
	}
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	if n.Visibility == "" {
		n.Visibility = "public"
	}
	if n.Type == "" {
		n.Type = "page"
	}

	// Calcul depth
	var depth int
	if n.ParentID.Valid && n.ParentID.String != "" {
		parent, err := s.GetByID(ctx, n.ParentID.String)
		if err != nil {
			return "", fmt.Errorf("tree.Create get parent: %w", err)
		}
		depth = parent.Depth + 1
	}
	n.Depth = depth

	n.BodyHTML = renderMD(n.BodyMD)
	now := time.Now().UTC()

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO nodes(id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID,
		nullStr(n.ParentID),
		n.Slug,
		n.Type,
		n.Title,
		n.BodyMD,
		n.BodyHTML,
		n.Visibility,
		nullStr(n.AuthorID),
		n.DisplayOrder,
		n.Depth,
		now.Format("2006-01-02 15:04:05"),
		now.Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrSlugTaken
		}
		return "", fmt.Errorf("tree.Create: %w", err)
	}
	return n.ID, nil
}

// GetByID retourne un nœud par son ID. Retourne ErrNotFound si absent ou soft-deleted.
func (s *Store) GetByID(ctx context.Context, id string) (*Node, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at
		 FROM nodes WHERE id=? AND deleted_at IS NULL`, id)
	return scanNode(row)
}

// GetBySlug retourne un nœud par son slug. Retourne ErrNotFound si absent.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Node, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at
		 FROM nodes WHERE slug=? AND deleted_at IS NULL`, slug)
	return scanNode(row)
}

// Update met à jour un nœud existant. Régénère body_html depuis body_md.
func (s *Store) Update(ctx context.Context, n Node) error {
	n.BodyHTML = renderMD(n.BodyMD)
	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET title=?, body_md=?, body_html=?, visibility=?, display_order=?, updated_at=?
		 WHERE id=? AND deleted_at IS NULL`,
		n.Title, n.BodyMD, n.BodyHTML, n.Visibility, n.DisplayOrder,
		now.Format("2006-01-02 15:04:05"), n.ID,
	)
	if err != nil {
		return fmt.Errorf("tree.Update: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDelete pose deleted_at sur le nœud et tous ses descendants (cascade).
func (s *Store) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err := s.DB.ExecContext(ctx, `
		WITH RECURSIVE descendants(id) AS (
			SELECT id FROM nodes WHERE id=? AND deleted_at IS NULL
			UNION ALL
			SELECT n.id FROM nodes n
			JOIN descendants d ON n.parent_id = d.id
			WHERE n.deleted_at IS NULL
		)
		UPDATE nodes SET deleted_at=? WHERE id IN (SELECT id FROM descendants)
	`, id, now)
	if err != nil {
		return fmt.Errorf("tree.SoftDelete: %w", err)
	}
	return nil
}

// Children retourne les enfants directs d'un nœud (non supprimés), triés par display_order.
func (s *Store) Children(ctx context.Context, parentID string) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at
		 FROM nodes WHERE parent_id=? AND deleted_at IS NULL ORDER BY display_order, created_at`, parentID)
	if err != nil {
		return nil, fmt.Errorf("tree.Children: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// Ancestors retourne les ancêtres d'un nœud de la racine vers le parent immédiat (breadcrumb).
func (s *Store) Ancestors(ctx context.Context, id string) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE anc(id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at) AS (
			SELECT n.id, n.parent_id, n.slug, n.type, n.title, n.body_md, n.body_html, n.visibility, n.author_id, n.display_order, n.depth, n.created_at, n.updated_at, n.deleted_at
			FROM nodes n WHERE n.id=(SELECT parent_id FROM nodes WHERE id=? AND deleted_at IS NULL)
			UNION ALL
			SELECT n.id, n.parent_id, n.slug, n.type, n.title, n.body_md, n.body_html, n.visibility, n.author_id, n.display_order, n.depth, n.created_at, n.updated_at, n.deleted_at
			FROM nodes n JOIN anc a ON n.id=(SELECT parent_id FROM nodes WHERE id=a.id)
			WHERE n.deleted_at IS NULL
		)
		SELECT * FROM anc ORDER BY depth ASC
	`, id)
	if err != nil {
		return nil, fmt.Errorf("tree.Ancestors: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// Subtree retourne tous les descendants jusqu'à maxDepth (inclus), racine incluse.
func (s *Store) Subtree(ctx context.Context, rootID string, maxDepth int) ([]Node, error) {
	root, err := s.GetByID(ctx, rootID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE sub(id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at) AS (
			SELECT id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at
			FROM nodes WHERE id=? AND deleted_at IS NULL
			UNION ALL
			SELECT n.id, n.parent_id, n.slug, n.type, n.title, n.body_md, n.body_html, n.visibility, n.author_id, n.display_order, n.depth, n.created_at, n.updated_at, n.deleted_at
			FROM nodes n JOIN sub s ON n.parent_id=s.id
			WHERE n.deleted_at IS NULL AND n.depth <= ?
		)
		SELECT * FROM sub ORDER BY depth, display_order
	`, rootID, root.Depth+maxDepth)
	if err != nil {
		return nil, fmt.Errorf("tree.Subtree: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// Roots retourne les nœuds racines (parent_id IS NULL, non supprimés).
func (s *Store) Roots(ctx context.Context) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, parent_id, slug, type, title, body_md, body_html, visibility, author_id, display_order, depth, created_at, updated_at, deleted_at
		 FROM nodes WHERE parent_id IS NULL AND deleted_at IS NULL ORDER BY display_order, created_at`)
	if err != nil {
		return nil, fmt.Errorf("tree.Roots: %w", err)
	}
	defer rows.Close()
	return scanNodes(rows)
}

// Reorder met à jour display_order des enfants d'un parent dans l'ordre fourni.
func (s *Store) Reorder(ctx context.Context, parentID string, orderedIDs []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("tree.Reorder begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for i, id := range orderedIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE nodes SET display_order=? WHERE id=? AND parent_id=? AND deleted_at IS NULL`,
			i, id, parentID,
		); err != nil {
			return fmt.Errorf("tree.Reorder update %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// CheckCycle vérifie que newParentID n'est pas un descendant de nodeID.
// Retourne ErrCycle si c'est le cas.
func (s *Store) CheckCycle(ctx context.Context, nodeID, newParentID string) error {
	if nodeID == newParentID {
		return ErrCycle
	}
	ancestors, err := s.Ancestors(ctx, newParentID)
	if err != nil {
		return fmt.Errorf("tree.CheckCycle ancestors: %w", err)
	}
	for _, a := range ancestors {
		if a.ID == nodeID {
			return ErrCycle
		}
	}
	// Vérifie aussi que newParentID n'est pas dans le subtree de nodeID
	sub, err := s.Subtree(ctx, nodeID, 100)
	if err != nil {
		return fmt.Errorf("tree.CheckCycle subtree: %w", err)
	}
	for _, n := range sub {
		if n.ID == newParentID {
			return ErrCycle
		}
	}
	return nil
}

// --- helpers ---

func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func scanNode(row *sql.Row) (*Node, error) {
	var n Node
	var createdAt, updatedAt string
	var deletedAt sql.NullString
	err := row.Scan(
		&n.ID, &n.ParentID, &n.Slug, &n.Type, &n.Title,
		&n.BodyMD, &n.BodyHTML, &n.Visibility, &n.AuthorID,
		&n.DisplayOrder, &n.Depth, &createdAt, &updatedAt, &deletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	n.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	if deletedAt.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", deletedAt.String)
		n.DeletedAt = sql.NullTime{Time: t, Valid: true}
	}
	return &n, nil
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	var result []Node
	for rows.Next() {
		var n Node
		var createdAt, updatedAt string
		var deletedAt sql.NullString
		err := rows.Scan(
			&n.ID, &n.ParentID, &n.Slug, &n.Type, &n.Title,
			&n.BodyMD, &n.BodyHTML, &n.Visibility, &n.AuthorID,
			&n.DisplayOrder, &n.Depth, &createdAt, &updatedAt, &deletedAt,
		)
		if err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		n.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		if deletedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", deletedAt.String)
			n.DeletedAt = sql.NullTime{Time: t, Valid: true}
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
