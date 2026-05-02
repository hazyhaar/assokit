// CLAUDE:SUMMARY Tests gardiens admin feedbacks : 403 non-admin, liste paginée, filtre status+search, triage persistance (M-ASSOKIT-FEEDBACK-F3).
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func newAdminDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	seedRoles(t, db)
	return app.AppDeps{DB: db, Logger: slog.Default()}
}

// seedFeedbacks insère n feedbacks de test avec statut donné, IDs uniques via status+index.
func seedFeedbacks(t *testing.T, deps app.AppDeps, n int, status string) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := "fb-test-" + status + "-" + feedbackItoa(i)
		msg := "Message de test numéro " + feedbackItoa(i) + " assez long"
		_, err := deps.DB.Exec(
			`INSERT INTO feedbacks(id, page_url, message, status) VALUES(?,?,?,?)`,
			id, "/page-"+feedbackItoa(i), msg, status,
		)
		if err != nil {
			t.Fatalf("seedFeedbacks[%d]: %v", i, err)
		}
	}
}

func feedbackItoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 8)
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func adminRequest(r *http.Request, user *auth.User) *http.Request {
	return r.WithContext(middleware.ContextWithUser(r.Context(), user))
}

// TestAdminFeedbacks_NonAdminReturns403 : user role=member → 403.
func TestAdminFeedbacks_NonAdminReturns403(t *testing.T) {
	deps := newAdminDeps(t)
	handler := requireAdmin(handleAdminFeedbackList(deps))

	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks", nil)
	member := &auth.User{ID: "u1", Roles: []string{"member"}}
	r = adminRequest(r, member)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("member → want 403, got %d", w.Code)
	}
}

// TestAdminFeedbacks_NilUserReturns403 : pas d'utilisateur → 403.
func TestAdminFeedbacks_NilUserReturns403(t *testing.T) {
	deps := newAdminDeps(t)
	handler := requireAdmin(handleAdminFeedbackList(deps))

	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks", nil)
	// pas de ContextWithUser → user nil
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("nil user → want 403, got %d", w.Code)
	}
}

// TestAdminFeedbacks_AdminReturns200 : admin → 200.
func TestAdminFeedbacks_AdminReturns200(t *testing.T) {
	deps := newAdminDeps(t)
	handler := requireAdmin(handleAdminFeedbackList(deps))

	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks", nil)
	admin := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, admin)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("admin → want 200, got %d\nbody: %s", w.Code, w.Body.String())
	}
}

// TestAdminFeedbacks_PaginationLimit50 : 60 feedbacks → page 1 affiche 50 items, total=60.
func TestAdminFeedbacks_PaginationLimit50(t *testing.T) {
	deps := newAdminDeps(t)
	seedFeedbacks(t, deps, 60, "pending")

	handler := handleAdminFeedbackList(deps)
	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks", nil)
	adminUser := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	// Le template affiche le total
	if !strings.Contains(body, "60") {
		t.Errorf("response should mention total 60, body excerpt: %q", body[:min(200, len(body))])
	}
	// 50 lignes = 50 occurrences de "fb-row-fb-test-pending-"
	count := strings.Count(body, "fb-row-fb-test-pending-")
	if count != 50 {
		t.Errorf("want 50 rows on page 1, got %d", count)
	}
}

// TestAdminFeedbacks_FilterByStatus : filtre status=spam → affiche seulement spam.
func TestAdminFeedbacks_FilterByStatus(t *testing.T) {
	deps := newAdminDeps(t)
	seedFeedbacks(t, deps, 3, "pending")
	seedFeedbacks(t, deps, 2, "spam")

	handler := handleAdminFeedbackList(deps)
	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks?status=spam", nil)
	adminUser := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// Total doit afficher 2
	body := w.Body.String()
	count := strings.Count(body, "fb-row-fb-test-")
	if count != 2 {
		t.Errorf("filter spam → want 2 rows, got %d", count)
	}
}

// TestAdminFeedbacks_FilterBySearch : search=numéro 1 → filtre sur message.
func TestAdminFeedbacks_FilterBySearch(t *testing.T) {
	deps := newAdminDeps(t)
	// Insérer quelques feedbacks avec messages distincts
	deps.DB.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES('s1','/a','Message unique alpha','pending')`)
	deps.DB.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES('s2','/b','Message unique beta','pending')`)
	deps.DB.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES('s3','/c','Autre texte sans mot clé','pending')`)

	handler := handleAdminFeedbackList(deps)
	form := url.Values{"search": {"unique alpha"}}
	r := httptest.NewRequest(http.MethodGet, "/admin/feedbacks?search=unique+alpha", nil)
	_ = form
	adminUser := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fb-row-s1") {
		t.Error("search 'unique alpha' devrait retourner s1")
	}
	if strings.Contains(body, "fb-row-s2") {
		t.Error("search 'unique alpha' ne devrait pas retourner s2 (beta)")
	}
	if strings.Contains(body, "fb-row-s3") {
		t.Error("search 'unique alpha' ne devrait pas retourner s3")
	}
}

// TestAdminFeedbacks_TriagePersistsFields : POST triage → UPDATE status + admin_note + triaged_by + triaged_at.
func TestAdminFeedbacks_TriagePersistsFields(t *testing.T) {
	deps := newAdminDeps(t)
	// Créer l'utilisateur admin (FK triaged_by REFERENCES users(id))
	if _, err := deps.DB.Exec(
		`INSERT INTO users(id,email,password_hash,display_name) VALUES('admin-user-1','admin@test.com','x','Admin')`,
	); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	deps.DB.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES('fb-triage','/x','Message assez long','pending')`)

	handler := handleAdminFeedbackTriage(deps)

	form := url.Values{
		"status":     {"triaged"},
		"admin_note": {"Vérifié, légitime."},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/feedbacks/fb-triage/triage",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	adminUser := &auth.User{ID: "admin-user-1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "fb-triage")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\nbody: %s", w.Code, w.Body.String())
	}

	var status, adminNote, triagedBy, triagedAt string
	err := deps.DB.QueryRow(
		`SELECT status, admin_note, COALESCE(triaged_by,''), COALESCE(triaged_at,'') FROM feedbacks WHERE id='fb-triage'`,
	).Scan(&status, &adminNote, &triagedBy, &triagedAt)
	if err != nil {
		t.Fatalf("SELECT triage: %v", err)
	}
	if status != "triaged" {
		t.Errorf("status = %q, want triaged", status)
	}
	if adminNote != "Vérifié, légitime." {
		t.Errorf("admin_note = %q, want 'Vérifié, légitime.'", adminNote)
	}
	if triagedBy != "admin-user-1" {
		t.Errorf("triaged_by = %q, want admin-user-1", triagedBy)
	}
	if triagedAt == "" {
		t.Error("triaged_at doit être non vide")
	}
}

// TestAdminFeedbacks_TriageInvalidStatus : status invalide → 400.
func TestAdminFeedbacks_TriageInvalidStatus(t *testing.T) {
	deps := newAdminDeps(t)
	deps.DB.Exec(`INSERT INTO feedbacks(id,page_url,message,status) VALUES('fb-bad','/x','Message valide assez long','pending')`)

	handler := handleAdminFeedbackTriage(deps)
	form := url.Values{"status": {"invalid"}, "admin_note": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/feedbacks/fb-bad/triage",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminUser := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "fb-bad")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid status → want 400, got %d", w.Code)
	}
}

// TestAdminFeedbacks_TriageNotFound : id inexistant → 404.
func TestAdminFeedbacks_TriageNotFound(t *testing.T) {
	deps := newAdminDeps(t)
	handler := handleAdminFeedbackTriage(deps)

	form := url.Values{"status": {"closed"}, "admin_note": {""}}
	r := httptest.NewRequest(http.MethodPost, "/admin/feedbacks/nonexistent/triage",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	adminUser := &auth.User{ID: "admin1", Roles: []string{"admin"}}
	r = adminRequest(r, adminUser)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent id → want 404, got %d", w.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
