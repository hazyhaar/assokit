// Package main — handler GET /feedback/comments pour le worker feedback-pull horos55.
// Expose la table feedbacks de la DB core en lecture seule, sans auth.
// Contrat RemoteComment : tableau JSON nu (array), champs id/text/page_url/screenshot_path/app_name/created_at.
package main

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
)

// remoteComment est la structure attendue par le worker feedback_pull horos55.
// Enveloppe : array JSON nu ([]remoteComment), conformément au parser fetchComments
// qui fait json.Unmarshal(body, &[]RemoteComment{}).
type remoteComment struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	PageURL        string `json:"page_url"`
	ScreenshotPath string `json:"screenshot_path"`
	AppName        string `json:"app_name"`
	CreatedAt      int64  `json:"created_at"`
}

// feedbackCommentsHandler sert GET /feedback/comments.
// db : handle RO sur la DB core assokit (contient la table feedbacks).
// appName : identifiant du silo, injecté dans chaque remoteComment.app_name.
type feedbackCommentsHandler struct {
	db      *sql.DB
	appName string
	logger  *slog.Logger
}

func (h *feedbackCommentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, page_url, message, CAST(strftime('%s', created_at) AS INTEGER)
		   FROM feedbacks
		  ORDER BY created_at ASC`,
	)
	if err != nil {
		h.logger.Error("feedback_comments: query", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := make([]remoteComment, 0)
	for rows.Next() {
		var c remoteComment
		if err := rows.Scan(&c.ID, &c.PageURL, &c.Text, &c.CreatedAt); err != nil {
			h.logger.Error("feedback_comments: scan", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		c.ScreenshotPath = "" // pas de capture screenshot aujourd'hui
		c.AppName = h.appName
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("feedback_comments: rows", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		h.logger.Error("feedback_comments: encode", "err", err)
	}
}
