// CLAUDE:SUMMARY FTS5 wrapper avec sanitization query + QueryFiltered par perms.
package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Hit représente un résultat de recherche.
type Hit struct {
	NodeID  string
	Title   string
	Snippet string
	Rank    float64
}

// Engine est le moteur de recherche FTS5.
type Engine struct {
	DB *sql.DB
}

// sanitizeQuery nettoie la query FTS5 pour éviter les erreurs de parsing.
// Enveloppe dans des guillemets si la query contient des caractères spéciaux.
func sanitizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	// Retire les caractères FTS5 opérateurs problématiques
	problematic := []string{`"`, `(`, `)`, `*`, `^`, `+`, `AND`, `OR`, `NOT`}
	for _, p := range problematic {
		if strings.Contains(strings.ToUpper(q), p) {
			// Échappe les guillemets et enveloppe
			q = strings.ReplaceAll(q, `"`, `""`)
			return `"` + q + `"`
		}
	}
	return q
}

// Query recherche dans tous les nœuds publics (sans filtre perms).
func (e *Engine) Query(ctx context.Context, q string, limit int) ([]Hit, error) {
	q = sanitizeQuery(q)
	if q == "" {
		return []Hit{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	// FTS5 external content : snippet/rank ne fonctionnent pas en JOIN.
	// On passe par une subquery rowid puis on récupère le contenu depuis nodes.
	rows, err := e.DB.QueryContext(ctx, `
		SELECT n.id, n.title, '' as snippet, 0.0 as rank
		FROM nodes n
		WHERE n.rowid IN (
			SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH ?
		)
		AND n.deleted_at IS NULL
		LIMIT ?
	`, q, limit)
	if err != nil {
		// Query FTS malformée → retourne vide plutôt que propager l'erreur SQL
		if isFTSSyntaxError(err) {
			return []Hit{}, nil
		}
		return nil, fmt.Errorf("search.Query: %w", err)
	}
	defer rows.Close()
	return scanHits(rows)
}

// QueryFiltered recherche en filtrant sur les nœuds lisibles par userRoles.
func (e *Engine) QueryFiltered(ctx context.Context, q string, userRoles []string, limit int) ([]Hit, error) {
	q = sanitizeQuery(q)
	if q == "" {
		return []Hit{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if len(userRoles) == 0 {
		return []Hit{}, nil
	}

	// Construit les placeholders pour les rôles
	ph := make([]string, len(userRoles))
	args := make([]interface{}, 0, len(userRoles)+2)
	args = append(args, q)
	for i, r := range userRoles {
		ph[i] = "?"
		args = append(args, r)
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT n.id, n.title, '' as snippet, 0.0 as rank
		FROM nodes n
		WHERE n.rowid IN (
			SELECT rowid FROM nodes_fts WHERE nodes_fts MATCH ?
		)
		AND n.deleted_at IS NULL
		AND n.id IN (
			SELECT DISTINCT np.node_id
			FROM node_permissions np
			WHERE np.role_id IN (%s)
			AND np.permission IN ('read','write','moderate','admin')
		)
		LIMIT ?
	`, strings.Join(ph, ","))

	rows, err := e.DB.QueryContext(ctx, query, args...)
	if err != nil {
		if isFTSSyntaxError(err) {
			return []Hit{}, nil
		}
		return nil, fmt.Errorf("search.QueryFiltered: %w", err)
	}
	defer rows.Close()
	return scanHits(rows)
}

func scanHits(rows *sql.Rows) ([]Hit, error) {
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.NodeID, &h.Title, &h.Snippet, &h.Rank); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	if hits == nil {
		return []Hit{}, rows.Err()
	}
	return hits, rows.Err()
}

func isFTSSyntaxError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "fts5:") || strings.Contains(err.Error(), "syntax error"))
}
