// CLAUDE:SUMMARY État lu/non-lu du forum par question via node_reads (upsert idempotent,
// "a du nouveau" = enfant created_at > last_read_at). Aucun rollup récursif d'arbre.
package forum

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// nodeReadFormat : même format que nodetree.Create pour created_at (UTC, seconde),
// afin que la comparaison textuelle last_read_at vs created_at soit chronologique.
const nodeReadFormat = "2006-01-02 15:04:05"

// MarkRead enregistre (ou avance) la dernière lecture d'un nœud (question) par un
// utilisateur. Idempotent : un second appel ne crée pas de ligne, il avance
// last_read_at. À n'appeler que depuis une requête non idempotente (POST).
func MarkRead(ctx context.Context, db *sql.DB, userID, nodeID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO node_reads(user_id, node_id, last_read_at) VALUES(?,?,?)
		 ON CONFLICT(user_id, node_id) DO UPDATE SET last_read_at=excluded.last_read_at`,
		userID, nodeID, time.Now().UTC().Format(nodeReadFormat),
	)
	return err
}

// UnreadSet retourne, parmi les questionIDs fournis, l'ensemble de ceux qui portent
// au moins un message enfant plus récent que la dernière lecture de l'utilisateur
// (ou jamais lus). Une seule requête, aucune récursion d'arbre. userID vide → map vide.
func UnreadSet(ctx context.Context, db *sql.DB, userID string, questionIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if userID == "" || len(questionIDs) == 0 {
		return out, nil
	}
	ph, args := placeholders(questionIDs)
	args = append([]any{userID}, args...)
	rows, err := db.QueryContext(ctx,
		`SELECT q.id
		 FROM nodes q
		 LEFT JOIN node_reads nr ON nr.node_id=q.id AND nr.user_id=?
		 WHERE q.id IN (`+ph+`) AND q.deleted_at IS NULL
		   AND EXISTS (SELECT 1 FROM nodes c
		               WHERE c.parent_id=q.id AND c.deleted_at IS NULL
		                 AND c.created_at > COALESCE(nr.last_read_at, ''))`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UnreadQuestions retourne, parmi les questionIDs, celles qui portent au moins un
// message (sous une de leurs BRANCHES) plus récent que la lecture de cette branche.
// Traversée à deux niveaux question→branche→message ; node_reads est ancré à la
// BRANCHE consultée (pas à la question). userID vide → map vide.
func UnreadQuestions(ctx context.Context, db *sql.DB, userID string, questionIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if userID == "" || len(questionIDs) == 0 {
		return out, nil
	}
	ph, args := placeholders(questionIDs)
	args = append([]any{userID}, args...)
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT q.id
		 FROM nodes q
		 JOIN nodes b ON b.parent_id=q.id AND b.deleted_at IS NULL
		 JOIN nodes m ON m.parent_id=b.id AND m.deleted_at IS NULL
		 LEFT JOIN node_reads nr ON nr.node_id=b.id AND nr.user_id=?
		 WHERE q.id IN (`+ph+`) AND m.created_at > COALESCE(nr.last_read_at, '')`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UnreadByCategory compte, pour chaque catégorie, le nombre de ses questions ayant
// au moins un message non lu (traversée catégorie→question→branche→message ; lecture
// ancrée à la branche). userID vide → map vide.
func UnreadByCategory(ctx context.Context, db *sql.DB, userID string, categoryIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if userID == "" || len(categoryIDs) == 0 {
		return out, nil
	}
	ph, args := placeholders(categoryIDs)
	args = append([]any{userID}, args...)
	rows, err := db.QueryContext(ctx,
		`SELECT q.parent_id, COUNT(DISTINCT q.id)
		 FROM nodes q
		 JOIN nodes b ON b.parent_id=q.id AND b.deleted_at IS NULL
		 JOIN nodes m ON m.parent_id=b.id AND m.deleted_at IS NULL
		 LEFT JOIN node_reads nr ON nr.node_id=b.id AND nr.user_id=?
		 WHERE q.parent_id IN (`+ph+`) AND q.deleted_at IS NULL
		   AND m.created_at > COALESCE(nr.last_read_at, '')
		 GROUP BY q.parent_id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var n int
		if err := rows.Scan(&cat, &n); err != nil {
			return nil, err
		}
		out[cat] = n
	}
	return out, rows.Err()
}

// placeholders construit "?,?,…" et la slice d'arguments associée.
func placeholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}
