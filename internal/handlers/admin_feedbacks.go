// CLAUDE:SUMMARY Handlers admin /admin/feedbacks — liste paginée, détail, triage (M-ASSOKIT-FEEDBACK-F3-ADMIN-UI-LIST-TRIAGE-PAGINATED).
package handlers

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/admin"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

const feedbackPageSize = 50

// requireAdmin est un middleware qui retourne 403 si l'utilisateur n'a pas le rôle admin.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil || !slices.Contains(u.Roles, "admin") {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleAdminFeedbackList — GET /admin/feedbacks : liste paginée avec filtres.
func handleAdminFeedbackList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filterStatus := q.Get("status")
		filterSearch := strings.TrimSpace(q.Get("search"))
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		offset := (page - 1) * feedbackPageSize

		// Construire la clause WHERE
		args := []any{}
		where := "WHERE 1=1"
		if filterStatus != "" {
			where += " AND status=?"
			args = append(args, filterStatus)
		}
		if filterSearch != "" {
			like := "%" + strings.ReplaceAll(filterSearch, "%", "\\%") + "%"
			where += " AND (message LIKE ? ESCAPE '\\' OR page_url LIKE ? ESCAPE '\\')"
			args = append(args, like, like)
		}

		// Total count
		var total int
		countArgs := make([]any, len(args))
		copy(countArgs, args)
		if err := deps.DB.QueryRowContext(r.Context(),
			"SELECT COUNT(*) FROM feedbacks "+where, countArgs...,
		).Scan(&total); err != nil {
			deps.Logger.Error("admin feedbacks count", "err", err)
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		// Rows
		listArgs := make([]any, len(args)+2)
		copy(listArgs, args)
		listArgs[len(args)] = feedbackPageSize
		listArgs[len(args)+1] = offset
		rows, err := deps.DB.QueryContext(r.Context(),
			"SELECT id, page_url, message, status, created_at FROM feedbacks "+
				where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
			listArgs...,
		)
		if err != nil {
			deps.Logger.Error("admin feedbacks list", "err", err)
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var fbRows []admin.FeedbackRow
		for rows.Next() {
			var fb admin.FeedbackRow
			var msg string
			if err := rows.Scan(&fb.ID, &fb.PageURL, &msg, &fb.Status, &fb.CreatedAt); err != nil {
				deps.Logger.Error("admin feedbacks scan", "err", err)
				continue
			}
			runes := []rune(msg)
			if len(runes) > 100 {
				fb.MessageSnip = string(runes[:100]) + "…"
			} else {
				fb.MessageSnip = msg
			}
			fbRows = append(fbRows, fb)
		}
		if err := rows.Err(); err != nil {
			deps.Logger.Error("admin feedbacks rows", "err", err)
		}

		// Construire baseQuery sans page=
		baseQ := ""
		if filterStatus != "" {
			baseQ += "status=" + filterStatus + "&"
		}
		if filterSearch != "" {
			baseQ += "search=" + filterSearch + "&"
		}

		pag := admin.PaginationInfo{
			Page:      page,
			Total:     total,
			HasPrev:   page > 1,
			HasNext:   offset+feedbackPageSize < total,
			PrevPage:  page - 1,
			NextPage:  page + 1,
			BaseQuery: baseQ,
		}
		filter := admin.FeedbackFilter{Status: filterStatus, Search: filterSearch}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := admin.FeedbackListPage(fbRows, pag, filter).Render(r.Context(), w); err != nil {
			deps.Logger.Error("admin feedbacks render", "err", err)
		}
	}
}

// handleAdminFeedbackDetail — GET /admin/feedbacks/{id}.
func handleAdminFeedbackDetail(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var fb admin.FeedbackDetail
		var ipHash, triagedBy, triagedAt string
		err := deps.DB.QueryRowContext(r.Context(),
			`SELECT id, page_url, COALESCE(page_title,''), message,
			        COALESCE(ip_hash,''), COALESCE(user_agent,''), COALESCE(locale,''),
			        status, COALESCE(admin_note,''),
			        COALESCE(triaged_by,''), COALESCE(triaged_at,''), created_at
			 FROM feedbacks WHERE id=?`, id,
		).Scan(
			&fb.ID, &fb.PageURL, &fb.PageTitle, &fb.Message,
			&ipHash, &fb.UserAgent, &fb.Locale,
			&fb.Status, &fb.AdminNote,
			&triagedBy, &triagedAt, &fb.CreatedAt,
		)
		if err != nil {
			http.Error(w, "Feedback introuvable", http.StatusNotFound)
			return
		}
		// ip_hash : afficher seulement les 4 derniers chars
		if len(ipHash) >= 4 {
			fb.IPHashSnip = ipHash[len(ipHash)-4:]
		}
		fb.TriagedBy = triagedBy
		fb.TriagedAt = triagedAt

		csrfToken := middleware.CSRFToken(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := admin.FeedbackDetailPage(fb, csrfToken).Render(r.Context(), w); err != nil {
			deps.Logger.Error("admin feedback detail render", "err", err)
		}
	}
}

// handleAdminFeedbackTriage — POST /admin/feedbacks/{id}/triage.
func handleAdminFeedbackTriage(deps app.AppDeps) http.HandlerFunc {
	validStatuses := map[string]bool{
		"pending": true, "triaged": true, "closed": true, "spam": true,
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		status := r.FormValue("status")
		if !validStatuses[status] {
			http.Error(w, "Statut invalide", http.StatusBadRequest)
			return
		}
		adminNote := r.FormValue("admin_note")

		u := middleware.UserFromContext(r.Context())
		triagedBy := ""
		if u != nil {
			triagedBy = u.ID
		}
		triagedAt := time.Now().UTC().Format("2006-01-02T15:04:05Z")

		res, err := deps.DB.ExecContext(r.Context(),
			`UPDATE feedbacks SET status=?, admin_note=?, triaged_by=?, triaged_at=?
			 WHERE id=?`,
			status, adminNote, triagedBy, triagedAt, id,
		)
		if err != nil {
			deps.Logger.Error("admin feedback triage", "err", err)
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.Error(w, "Feedback introuvable", http.StatusNotFound)
			return
		}

		// HTMX : répondre avec la ligne mise à jour.
		var msg string
		var fb admin.FeedbackRow
		_ = deps.DB.QueryRowContext(r.Context(),
			`SELECT id, page_url, message, status, created_at FROM feedbacks WHERE id=?`, id,
		).Scan(&fb.ID, &fb.PageURL, &msg, &fb.Status, &fb.CreatedAt)
		runes := []rune(msg)
		if len(runes) > 100 {
			fb.MessageSnip = string(runes[:100]) + "…"
		} else {
			fb.MessageSnip = msg
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := admin.FeedbackListRow(fb).Render(r.Context(), w); err != nil {
			deps.Logger.Error("admin feedback triage render", "err", err)
		}
	}
}
