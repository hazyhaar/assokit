package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

// TestForumIndex_TitleUsesNavLabel : le titre de /forum reprend le libellé nav
// (ex. « Dossiers » sous GAFP), pas le libellé générique « Forum ».
func TestForumIndex_TitleUsesNavLabel(t *testing.T) {
	theme.Init(&theme.Branding{
		Name:    "GAFP Test",
		BaseURL: "http://localhost",
		Nav: []theme.NavItem{
			{Slug: "/forum", Label: "Dossiers", Order: 1},
		},
		Texts: map[string]string{},
	})

	db := newTestDB(t)
	defer db.Close()
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	r := httptest.NewRequest(http.MethodGet, "/forum", nil)
	w := httptest.NewRecorder()
	handleForumIndex(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, obtenu %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<title>Dossiers — GAFP Test</title>") {
		t.Fatalf("titre page attendu « Dossiers », got extrait sans match dans %q", body[:min(500, len(body))])
	}
	if strings.Contains(body, "Forum communautaire") {
		t.Fatal("titre ne doit pas afficher « Forum communautaire »")
	}
}
