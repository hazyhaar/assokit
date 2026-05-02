package adminpanel_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	adminpanel "github.com/hazyhaar/assokit/internal/handlers/admin_panel"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func newAdminDeps(t *testing.T) app.AppDeps {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return app.AppDeps{DB: db, Logger: slog.Default()}
}

func withAdminUser(r *http.Request) *http.Request {
	u := &auth.User{ID: "", Email: "admin@test.com", Roles: []string{"admin"}}
	return r.WithContext(middleware.ContextWithUser(r.Context(), u))
}

func newRouter(deps app.AppDeps) chi.Router {
	r := chi.NewRouter()
	adminpanel.Mount(r, deps)
	return r
}

// TestAdminPanel_NonAdminReturns403 : GET /admin/panel sans auth → 403.
func TestAdminPanel_NonAdminReturns403(t *testing.T) {
	deps := newAdminDeps(t)
	r := newRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/admin/panel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("attendu 403, got %d", w.Code)
	}
}

// TestAdminPanel_AutoSavePersists : POST save-field → DB contient la valeur.
func TestAdminPanel_AutoSavePersists(t *testing.T) {
	deps := newAdminDeps(t)
	r := newRouter(deps)

	form := url.Values{}
	form.Set("key", "identite.nom_asso")
	form.Set("value", "Association Test")

	req := httptest.NewRequest(http.MethodPost, "/admin/panel/save-field", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withAdminUser(req)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("attendu 200, got %d — body: %s", w.Code, w.Body.String())
	}

	// Vérifier en DB
	var val string
	err := deps.DB.QueryRowContext(context.Background(),
		`SELECT value FROM branding_kv WHERE key='identite.nom_asso'`,
	).Scan(&val)
	if err != nil {
		t.Fatalf("SELECT branding_kv: %v", err)
	}
	if val != "Association Test" {
		t.Errorf("valeur DB : attendu %q, got %q", "Association Test", val)
	}
}

// TestAdminPanel_ProgressBarCountsRequiredOnly : INSERT 1 required + 1 optional → progress correct.
func TestAdminPanel_ProgressBarCountsRequiredOnly(t *testing.T) {
	deps := newAdminDeps(t)
	r := newRouter(deps)

	// Insérer 1 champ required (nom_asso) et 1 optional (sigle)
	for _, kv := range []struct{ k, v string }{
		{"identite.nom_asso", "Asso X"},  // required
		{"identite.sigle", "AX"},         // optional
	} {
		form := url.Values{}
		form.Set("key", kv.k)
		form.Set("value", kv.v)
		req := httptest.NewRequest(http.MethodPost, "/admin/panel/save-field", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = withAdminUser(req)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("save %s: attendu 200, got %d", kv.k, w.Code)
		}
	}

	// GET /admin/panel/progress
	req := httptest.NewRequest(http.MethodGet, "/admin/panel/progress", nil)
	req = withAdminUser(req)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("progress: attendu 200, got %d", w.Code)
	}

	body := w.Body.String()
	// required_filled doit être 1 (seul nom_asso, required, est rempli)
	if !strings.Contains(body, `"required_filled":1`) {
		t.Errorf("required_filled attendu 1 dans: %s", body)
	}
}

// TestAdminPanel_FileUploadStoresAndServes : POST upload-file avec un PNG → badge + file_path en DB.
func TestAdminPanel_FileUploadStoresAndServes(t *testing.T) {
	deps := newAdminDeps(t)
	uploadDir := t.TempDir()
	t.Setenv("BRANDING_DIR", uploadDir)
	r := newRouter(deps)

	// Construire un multipart avec un faux PNG (magic bytes suffisent pour DetectContentType)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("key", "presentation.og_image")
	fw, err := mw.CreateFormFile("file", "og.png")
	if err != nil {
		t.Fatalf("createFormFile: %v", err)
	}
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	fw.Write(append(pngMagic, make([]byte, 64)...)) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/panel/upload-file", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withAdminUser(req)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload-file : attendu 200, got %d — %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "enregistré") {
		t.Errorf("upload-file : badge 'enregistré' attendu dans: %s", w.Body.String())
	}

	// Vérifier stockage en DB
	var fp string
	err = deps.DB.QueryRowContext(context.Background(),
		`SELECT COALESCE(file_path,'') FROM branding_kv WHERE key='presentation.og_image'`,
	).Scan(&fp)
	if err != nil || fp == "" {
		t.Errorf("upload-file : file_path absent de branding_kv (err=%v, fp=%q)", err, fp)
	}
}

// TestAdminPanel_IBANValidationCorrect : POST save-field avec IBAN invalide → 400.
func TestAdminPanel_IBANValidationCorrect(t *testing.T) {
	deps := newAdminDeps(t)
	r := newRouter(deps)

	form := url.Values{}
	form.Set("key", "virement.iban")
	form.Set("value", "INVALIDIBAN00000000000")

	req := httptest.NewRequest(http.MethodPost, "/admin/panel/save-field", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withAdminUser(req)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("IBAN invalide attendu 400, got %d", w.Code)
	}
}
