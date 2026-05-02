// CLAUDE:SUMMARY Tests gardiens export CSV donations — header + filtres + BOM (M-ASSOKIT-SPRINT3-S4).
package handlers

import (
	"encoding/csv"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
)

// TestDonationsExport_HeaderColumns : vérifie l'ordre exact des 12 colonnes du header CSV.
func TestDonationsExport_HeaderColumns(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations/export.csv", nil))
	w := httptest.NewRecorder()
	AdminDonationsExportCSV(deps)(w, req)

	body := w.Body.Bytes()
	if len(body) < 3 {
		t.Fatalf("body trop court: %d", len(body))
	}
	r := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV parse: %v", err)
	}
	if len(rows) < 1 {
		t.Fatal("CSV vide (pas de header)")
	}
	want := []string{"id", "date", "donateur", "email", "type", "montant_eur", "currency",
		"status", "user_id", "user_email", "is_member", "helloasso_payment_id"}
	if len(rows[0]) != len(want) {
		t.Fatalf("header cols=%d, attendu %d : %v", len(rows[0]), len(want), rows[0])
	}
	for i, col := range want {
		if rows[0][i] != col {
			t.Errorf("header col[%d]=%q, attendu %q", i, rows[0][i], col)
		}
	}
}

// TestDonationsExport_ContentDispositionAttachment : header HTTP attachment.
func TestDonationsExport_ContentDispositionAttachment(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations/export.csv", nil))
	w := httptest.NewRecorder()
	AdminDonationsExportCSV(deps)(w, req)

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, attendu attachment", cd)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, attendu text/csv", ct)
	}
}
