// CLAUDE:SUMMARY Handlers admin /admin/rbac — grades, permissions, users, audit RBAC (M-ASSOKIT-RBAC-4).
package handlers

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	adminrbac "github.com/hazyhaar/assokit/pkg/horui/admin/rbac"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
)

const auditPageSize = 50

// requireRBACAdmin vérifie que l'user a le rôle admin avant d'accéder à /admin/rbac/*.
func requireRBACAdmin(_ *rbac.Service) func(http.Handler) http.Handler {
	return requireAdmin
}

// mountRBACAdminRoutes câble les 9 routes /admin/rbac/* avec leurs perms.
func mountRBACAdminRoutes(r chi.Router, deps app.AppDeps, svc *rbac.Service) {
	r.With(perms.Required("rbac.grades.read")).Get("/admin/rbac/grades", handleAdminRBACGradesList(deps, svc))
	r.With(perms.Required("rbac.grades.write")).Post("/admin/rbac/grades", handleAdminRBACGradesCreate(deps, svc))
	r.With(perms.Required("rbac.grades.read")).Get("/admin/rbac/grades/{id}", handleAdminRBACGradeEdit(deps, svc))
	r.With(perms.Required("rbac.grades.write")).Post("/admin/rbac/grades/{id}/perms", handleAdminRBACGradePerms(deps, svc))
	r.With(perms.Required("rbac.grades.write")).Post("/admin/rbac/grades/{id}/inherit", handleAdminRBACGradeInherit(deps, svc))
	r.With(perms.Required("rbac.grades.write")).Delete("/admin/rbac/grades/{id}", handleAdminRBACGradeDelete(deps, svc))
	r.With(perms.Required("rbac.users.read")).Get("/admin/rbac/users", handleAdminRBACUsersList(deps, svc))
	r.With(perms.Required("rbac.users.write")).Post("/admin/rbac/users/{id}/grades", handleAdminRBACUserGradeAssign(deps, svc))
	r.With(perms.Required("rbac.users.write")).Delete("/admin/rbac/users/{id}/grades/{gid}", handleAdminRBACUserGradeRemove(deps, svc))
	r.With(perms.Required("rbac.audit.read")).Get("/admin/rbac/audit", handleAdminRBACAuditList(deps))
}

// handleAdminRBACGradesList — GET /admin/rbac/grades : liste grades + compteurs.
func handleAdminRBACGradesList(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.DB.QueryContext(r.Context(), `
			SELECT g.id, g.name, g.system,
				COUNT(DISTINCT ug.user_id),
				COUNT(DISTINCT gp.permission_id)
			FROM grades g
			LEFT JOIN user_grades ug ON ug.grade_id = g.id
			LEFT JOIN grade_permissions gp ON gp.grade_id = g.id
			GROUP BY g.id ORDER BY g.name`)
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		var gradeRows []adminrbac.GradeRow
		for rows.Next() {
			var g adminrbac.GradeRow
			var sys int
			if err := rows.Scan(&g.ID, &g.Name, &sys, &g.UsersCount, &g.PermsCount); err != nil {
				continue
			}
			g.IsSystem = sys == 1
			gradeRows = append(gradeRows, g)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminrbac.GradeListPage(gradeRows).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleAdminRBACGradesCreate — POST /admin/rbac/grades : créer un grade.
func handleAdminRBACGradesCreate(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			http.Error(w, "Nom requis", http.StatusBadRequest)
			return
		}
		id, err := svc.Store.CreateGrade(r.Context(), name)
		if err != nil {
			http.Error(w, "Erreur création grade", http.StatusInternalServerError)
			return
		}
		g := adminrbac.GradeRow{ID: id, Name: name}
		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			adminrbac.GradeListRow(g).Render(r.Context(), w) //nolint:errcheck
			return
		}
		http.Redirect(w, r, "/admin/rbac/grades", http.StatusSeeOther)
	}
}

// handleAdminRBACGradeEdit — GET /admin/rbac/grades/{id} : formulaire édition.
func handleAdminRBACGradeEdit(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		grade, err := svc.Store.GetGrade(r.Context(), id)
		if errors.Is(err, rbac.ErrNotFound) {
			http.Error(w, "Grade introuvable", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		allPerms, _ := svc.Store.ListPermissions(r.Context())
		gradePerms, _ := svc.Store.GradePermissions(r.Context(), id)
		hasPerms := make(map[string]bool, len(gradePerms))
		for _, p := range gradePerms {
			hasPerms[p.ID] = true
		}
		permRows := make([]adminrbac.PermRow, 0, len(allPerms))
		for _, p := range allPerms {
			permRows = append(permRows, adminrbac.PermRow{ID: p.ID, Name: p.Name, Desc: p.Description})
		}
		allGrades, _ := svc.Store.ListGrades(r.Context())
		gradeRows := make([]adminrbac.GradeRow, 0, len(allGrades))
		for _, g := range allGrades {
			gradeRows = append(gradeRows, adminrbac.GradeRow{ID: g.ID, Name: g.Name, IsSystem: g.System})
		}
		parentRows, _ := deps.DB.QueryContext(r.Context(), `SELECT parent_id FROM grade_inherits WHERE child_id = ?`, id)
		defer parentRows.Close()
		parents := make(map[string]bool)
		for parentRows.Next() {
			var pid string
			parentRows.Scan(&pid) //nolint:errcheck
			parents[pid] = true
		}
		detail := adminrbac.GradeDetail{
			ID:        grade.ID,
			Name:      grade.Name,
			IsSystem:  grade.System,
			AllPerms:  permRows,
			HasPerms:  hasPerms,
			AllGrades: gradeRows,
			Parents:   parents,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminrbac.GradeEditPage(detail).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleAdminRBACGradePerms — POST /admin/rbac/grades/{id}/perms : sync permissions (toggle).
func handleAdminRBACGradePerms(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		newPerms := r.Form["perm_id"]
		current, _ := svc.Store.GradePermissions(r.Context(), id)
		for _, p := range current {
			if !slices.Contains(newPerms, p.ID) {
				svc.RevokePerm(r.Context(), id, p.ID) //nolint:errcheck
			}
		}
		for _, pid := range newPerms {
			svc.GrantPerm(r.Context(), id, pid) //nolint:errcheck
		}
		if r.Header.Get("HX-Request") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/admin/rbac/grades/"+id, http.StatusSeeOther)
	}
}

// handleAdminRBACGradeInherit — POST /admin/rbac/grades/{id}/inherit : sync héritage.
func handleAdminRBACGradeInherit(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		newParents := r.Form["parent_id"]
		parentRows, _ := deps.DB.QueryContext(r.Context(), `SELECT parent_id FROM grade_inherits WHERE child_id = ?`, id)
		defer parentRows.Close()
		var oldParents []string
		for parentRows.Next() {
			var pid string
			parentRows.Scan(&pid) //nolint:errcheck
			oldParents = append(oldParents, pid)
		}
		for _, pid := range oldParents {
			if !slices.Contains(newParents, pid) {
				svc.RemoveInherit(r.Context(), id, pid) //nolint:errcheck
			}
		}
		for _, pid := range newParents {
			if !slices.Contains(oldParents, pid) {
				if err := svc.AddInherit(r.Context(), id, pid); errors.Is(err, rbac.ErrCycleDetected) {
					http.Error(w, "Héritage créerait un cycle", http.StatusBadRequest)
					return
				}
			}
		}
		if r.Header.Get("HX-Request") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/admin/rbac/grades/"+id, http.StatusSeeOther)
	}
}

// handleAdminRBACGradeDelete — DELETE /admin/rbac/grades/{id}.
func handleAdminRBACGradeDelete(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		err := svc.Store.DeleteGrade(r.Context(), id)
		switch {
		case errors.Is(err, rbac.ErrSystemGrade):
			http.Error(w, "Suppression grade système interdite", http.StatusForbidden)
			return
		case errors.Is(err, rbac.ErrNotFound):
			http.Error(w, "Grade introuvable", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "Erreur suppression", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("HX-Request") != "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/admin/rbac/grades", http.StatusSeeOther)
	}
}

// handleAdminRBACUsersList — GET /admin/rbac/users : liste users + grades RBAC.
func handleAdminRBACUsersList(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		search := strings.TrimSpace(q.Get("search"))
		gradeFilter := q.Get("grade_id")

		where := "WHERE 1=1"
		var args []any
		if search != "" {
			where += " AND u.email LIKE ?"
			args = append(args, "%"+search+"%")
		}
		if gradeFilter != "" {
			where += " AND ug.grade_id = ?"
			args = append(args, gradeFilter)
		}
		args = append(args, 50)

		userRows, err := deps.DB.QueryContext(r.Context(), `
			SELECT u.id, u.email,
				COALESCE(GROUP_CONCAT(g.name, ','), '') as grade_names
			FROM users u
			LEFT JOIN user_grades ug ON ug.user_id = u.id
			LEFT JOIN grades g ON g.id = ug.grade_id
			`+where+`
			GROUP BY u.id ORDER BY u.email LIMIT ?`, args...)
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		defer userRows.Close()
		var users []adminrbac.UserRow
		for userRows.Next() {
			var u adminrbac.UserRow
			var gradeNames string
			if err := userRows.Scan(&u.ID, &u.Email, &gradeNames); err != nil {
				continue
			}
			if gradeNames != "" {
				u.Grades = strings.Split(gradeNames, ",")
			}
			users = append(users, u)
		}
		allGrades, _ := svc.Store.ListGrades(r.Context())
		gradeRows := make([]adminrbac.GradeRow, 0, len(allGrades))
		for _, g := range allGrades {
			gradeRows = append(gradeRows, adminrbac.GradeRow{ID: g.ID, Name: g.Name, IsSystem: g.System})
		}
		filter := adminrbac.UserFilter{Search: search, GradeID: gradeFilter}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminrbac.UserListPage(users, filter, gradeRows).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleAdminRBACUserGradeAssign — POST /admin/rbac/users/{id}/grades.
func handleAdminRBACUserGradeAssign(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "id")
		gradeID := r.FormValue("grade_id")
		if gradeID == "" {
			http.Error(w, "grade_id requis", http.StatusBadRequest)
			return
		}
		if err := svc.AssignGrade(r.Context(), userID, gradeID); err != nil {
			http.Error(w, "Erreur assignation", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("HX-Request") != "" {
			grades, _ := svc.Store.UserGrades(r.Context(), userID)
			gradeNames := make([]string, 0, len(grades))
			for _, g := range grades {
				gradeNames = append(gradeNames, g.Name)
			}
			var email string
			deps.DB.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=?`, userID).Scan(&email) //nolint:errcheck
			allGrades, _ := svc.Store.ListGrades(r.Context())
			allGradeRows := make([]adminrbac.GradeRow, 0, len(allGrades))
			for _, g := range allGrades {
				allGradeRows = append(allGradeRows, adminrbac.GradeRow{ID: g.ID, Name: g.Name, IsSystem: g.System})
			}
			u := adminrbac.UserRow{ID: userID, Email: email, Grades: gradeNames}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			adminrbac.UserListRow(u, allGradeRows).Render(r.Context(), w) //nolint:errcheck
			return
		}
		http.Redirect(w, r, "/admin/rbac/users", http.StatusSeeOther)
	}
}

// handleAdminRBACUserGradeRemove — DELETE /admin/rbac/users/{id}/grades/{gid}.
func handleAdminRBACUserGradeRemove(deps app.AppDeps, svc *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "id")
		gradeID := chi.URLParam(r, "gid")
		if err := svc.RemoveGrade(r.Context(), userID, gradeID); err != nil {
			http.Error(w, "Erreur retrait grade", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/rbac/users", http.StatusSeeOther)
	}
}

// handleAdminRBACAuditList — GET /admin/rbac/audit : liste audit paginée.
func handleAdminRBACAuditList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filterAction := strings.TrimSpace(q.Get("action"))
		filterActor := strings.TrimSpace(q.Get("actor_id"))
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		offset := (page - 1) * auditPageSize

		where := "WHERE 1=1"
		var args []any
		if filterAction != "" {
			where += " AND action = ?"
			args = append(args, filterAction)
		}
		if filterActor != "" {
			where += " AND actor_id = ?"
			args = append(args, filterActor)
		}
		var total int
		countArgs := make([]any, len(args))
		copy(countArgs, args)
		deps.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM rbac_audit "+where, countArgs...).Scan(&total) //nolint:errcheck

		listArgs := append(args, auditPageSize, offset) //nolint:gocritic
		rows, err := deps.DB.QueryContext(r.Context(),
			"SELECT id, action, actor_id, target_id, detail, created_at FROM rbac_audit "+
				where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", listArgs...)
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		var auditRows []adminrbac.AuditRow
		for rows.Next() {
			var a adminrbac.AuditRow
			if err := rows.Scan(&a.ID, &a.Action, &a.ActorID, &a.TargetID, &a.Detail, &a.CreatedAt); err != nil {
				continue
			}
			auditRows = append(auditRows, a)
		}
		pag := adminrbac.AuditPagination{
			Page:     page,
			Total:    total,
			HasPrev:  page > 1,
			HasNext:  offset+auditPageSize < total,
			PrevPage: page - 1,
			NextPage: page + 1,
		}
		filter := adminrbac.AuditFilter{Action: filterAction, ActorID: filterActor}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		adminrbac.AuditListPage(auditRows, pag, filter).Render(r.Context(), w) //nolint:errcheck
	}
}
