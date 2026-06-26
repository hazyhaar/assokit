// CLAUDE:SUMMARY Journal d'accès nominatif append-only : Store Log/ListBySubject/ListForActor + helper transverse LogAccess (warn-loud, n'échoue jamais la requête observée). DB injecté, single-writer base assokit. Import-clean (publiable).
package gdpr

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

const tsLayout = "2006-01-02 15:04:05"

// Store est le dépôt du journal des accès nominatifs. La connexion lui est
// injectée : il n'ouvre jamais la sienne et ne lit aucune variable
// d'environnement. Le journal est append-only — aucune méthode d'update/delete.
type Store struct {
	DB *sql.DB
}

// Log écrit une entrée dans le journal et retourne son identifiant. userID peut
// être vide (sujet non rattaché à un compte connu) ; subjectKind, subjectID,
// actorID et action sont requis. Append-only : aucun upsert.
func (s *Store) Log(ctx context.Context, e AccessLog) (string, error) {
	e.SubjectKind = strings.TrimSpace(e.SubjectKind)
	e.SubjectID = strings.TrimSpace(e.SubjectID)
	e.ActorID = strings.TrimSpace(e.ActorID)
	e.Action = strings.TrimSpace(e.Action)
	if e.SubjectKind == "" || e.SubjectID == "" || e.ActorID == "" || e.Action == "" {
		return "", fmt.Errorf("gdpr.Log: subject_kind, subject_id, actor_id et action requis")
	}

	id := uid.New()
	now := time.Now().UTC().Format(tsLayout)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO data_access_log(id, user_id, subject_kind, subject_id, actor_id, action, created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id, nullString(e.UserID), e.SubjectKind, e.SubjectID, e.ActorID, e.Action, now,
	)
	if err != nil {
		return "", fmt.Errorf("gdpr.Log: %w", err)
	}
	return id, nil
}

// ListBySubject retourne les accès portant sur un sujet donné, du plus récent au
// plus ancien (vue de redevabilité : qui a consulté cette personne / parcelle).
func (s *Store) ListBySubject(ctx context.Context, subjectKind, subjectID string) ([]AccessLog, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, subject_kind, subject_id, actor_id, action, created_at
		FROM data_access_log
		WHERE subject_kind = ? AND subject_id = ?
		ORDER BY created_at DESC
	`, subjectKind, subjectID)
	if err != nil {
		return nil, fmt.Errorf("gdpr.ListBySubject: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// ListForActor retourne les accès effectués par un acteur donné, du plus récent
// au plus ancien (vue de redevabilité : ce qu'un administrateur a consulté).
func (s *Store) ListForActor(ctx context.Context, actorID string) ([]AccessLog, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, subject_kind, subject_id, actor_id, action, created_at
		FROM data_access_log
		WHERE actor_id = ?
		ORDER BY created_at DESC
	`, actorID)
	if err != nil {
		return nil, fmt.Errorf("gdpr.ListForActor: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// ListRecent retourne les N derniers accès journalisés (vue admin globale).
func (s *Store) ListRecent(ctx context.Context, limit int) ([]AccessLog, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, subject_kind, subject_id, actor_id, action, created_at
		FROM data_access_log
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("gdpr.ListRecent: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// LogAccess est le point d'entrée transverse appelé sur un accès nominatif réel.
// Il est conçu pour être invoqué APRÈS le succès de l'accès métier qu'il observe :
// une erreur d'écriture du journal ne doit jamais faire échouer la requête
// métier. La doctrine est « warn-loud, jamais silent » : un échec d'insertion est
// journalisé en Warn (observable), sans propager l'erreur à l'appelant.
//
// store ou son DB nil → no-op silencieux (montage de test minimal sans journal).
func LogAccess(ctx context.Context, store *Store, logger *slog.Logger, e AccessLog) {
	if store == nil || store.DB == nil {
		return
	}
	id, err := store.Log(ctx, e)
	if err != nil {
		if logger != nil {
			logger.Warn("gdpr_access_log_failed",
				"subject_kind", e.SubjectKind,
				"subject_id", e.SubjectID,
				"actor_id", e.ActorID,
				"action", e.Action,
				"err", err.Error(),
			)
		}
		return
	}
	if logger != nil {
		logger.Info("gdpr_access_logged",
			"id", id,
			"subject_kind", e.SubjectKind,
			"subject_id", e.SubjectID,
			"actor_id", e.ActorID,
			"action", e.Action,
		)
	}
}

func scanLogs(rows *sql.Rows) ([]AccessLog, error) {
	var result []AccessLog
	for rows.Next() {
		var e AccessLog
		var userID sql.NullString
		var createdAt string
		if err := rows.Scan(&e.ID, &userID, &e.SubjectKind, &e.SubjectID, &e.ActorID, &e.Action, &createdAt); err != nil {
			return nil, err
		}
		if userID.Valid {
			e.UserID = userID.String
		}
		e.At, _ = time.Parse(tsLayout, createdAt)
		result = append(result, e)
	}
	return result, rows.Err()
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
