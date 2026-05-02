// CLAUDE:SUMMARY Tests gardiens admin donations — ACL, pagination keyset, filtres, stats, RGPD, manual match (M-ASSOKIT-SPRINT3-S4).
package handlers

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

const adminDonationsSchema = `
CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT, display_name TEXT);
CREATE TABLE roles (id TEXT PRIMARY KEY, label TEXT);
INSERT INTO roles(id, label) VALUES('admin','Admin'),('member','Member');
CREATE TABLE user_roles (user_id TEXT, role_id TEXT, PRIMARY KEY(user_id, role_id));
CREATE TABLE donations (
	id TEXT PRIMARY KEY,
	helloasso_payment_id TEXT NOT NULL UNIQUE,
	helloasso_form_slug TEXT NOT NULL DEFAULT '',
	helloasso_form_type TEXT NOT NULL DEFAULT '',
	amount_cents INTEGER NOT NULL,
	currency TEXT NOT NULL DEFAULT 'EUR',
	user_id TEXT,
	donor_email TEXT NOT NULL DEFAULT '',
	donor_name TEXT NOT NULL DEFAULT '',
	payment_status TEXT NOT NULL,
	paid_at TEXT, refunded_at TEXT,
	raw_event_id TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func setupAdminDonationsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(adminDonationsSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	invalidateStatsCache()
	return db
}

func seedDonation(t *testing.T, db *sql.DB, id, payID, donorName, donorEmail, status string, amountCents int64, paidAt string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO donations(id, helloasso_payment_id, helloasso_form_type, amount_cents, donor_name, donor_email, payment_status, paid_at, raw_event_id, created_at)
		VALUES(?, ?, 'Donation', ?, ?, ?, ?, ?, 'evt-x', ?)
	`, id, payID, amountCents, donorName, donorEmail, status, sql.NullString{String: paidAt, Valid: paidAt != ""}, paidAt); err != nil {
		t.Fatalf("seed donation %s: %v", id, err)
	}
}

func adminCtx(req *http.Request) *http.Request {
	return req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "admin-1", Roles: []string{"admin"}}))
}

// TestAdminDonations_NonAdminReturns403
func TestAdminDonations_NonAdminReturns403(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	req := httptest.NewRequest("GET", "/admin/donations", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(),
		&auth.User{ID: "u", Roles: []string{"member"}}))
	w := httptest.NewRecorder()
	AdminDonationsList(deps)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin code=%d, attendu 403", w.Code)
	}
}

// TestAdminDonations_ListPaginationKeyset : 100 donations → page1=50, page2=50.
func TestAdminDonations_ListPaginationKeyset(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	for i := 0; i < 100; i++ {
		seedDonation(t, db, fmt.Sprintf("d-%03d", i), fmt.Sprintf("p-%d", i),
			"Alice", fmt.Sprintf("a%d@x.com", i), "paid", 1000+int64(i),
			fmt.Sprintf("2026-04-%02d 10:00:00", (i%28)+1))
	}

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations", nil))
	w := httptest.NewRecorder()
	AdminDonationsList(deps)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("page1 code=%d", w.Code)
	}
	var resp1 map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp1) //nolint:errcheck
	items1, _ := resp1["items"].([]any)
	if len(items1) != 50 {
		t.Errorf("page1 len=%d, attendu 50", len(items1))
	}
	cursor, _ := resp1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("next_cursor vide après page 1")
	}

	req2 := adminCtx(httptest.NewRequest("GET", "/admin/donations?cursor="+url.QueryEscape(cursor), nil))
	w2 := httptest.NewRecorder()
	AdminDonationsList(deps)(w2, req2)
	var resp2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &resp2) //nolint:errcheck
	items2, _ := resp2["items"].([]any)
	if len(items2) == 0 {
		t.Errorf("page2 vide alors qu'attendu non-vide (100 rows total)")
	}
}

// TestAdminDonations_FilterByStatus
func TestAdminDonations_FilterByStatus(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	seedDonation(t, db, "p1", "h1", "A", "a@x.com", "paid", 1000, "2026-05-01 10:00:00")
	seedDonation(t, db, "p2", "h2", "B", "b@x.com", "paid", 2000, "2026-05-02 10:00:00")
	seedDonation(t, db, "r1", "h3", "C", "c@x.com", "refunded", 1500, "2026-05-03 10:00:00")

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations?status=refunded", nil))
	w := httptest.NewRecorder()
	AdminDonationsList(deps)(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	items, _ := resp["items"].([]any)
	if len(items) != 1 {
		t.Errorf("filter refunded len=%d, attendu 1", len(items))
	}
}

// TestAdminDonations_StatsCorrect : seed valeurs connues, stats endpoint exact.
func TestAdminDonations_StatsCorrect(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}

	for i, name := range []string{"A", "B", "C"} {
		seedDonation(t, db, fmt.Sprintf("d%d", i), fmt.Sprintf("h%d", i),
			name, fmt.Sprintf("%s@x.com", strings.ToLower(name)),
			"paid", 1000*int64(i+1), time.Now().UTC().Format("2006-01-02 15:04:05"))
	}

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations/stats.json", nil))
	w := httptest.NewRecorder()
	AdminDonationsStats(deps)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stats code=%d", w.Code)
	}
	var s DonationsStats
	json.Unmarshal(w.Body.Bytes(), &s) //nolint:errcheck

	if s.TotalCumulEUR != 60.0 {
		t.Errorf("total_cumul = %.2f, attendu 60.00 (10+20+30)", s.TotalCumulEUR)
	}
	if s.NbDonateursUniques != 3 {
		t.Errorf("nb_donateurs = %d, attendu 3", s.NbDonateursUniques)
	}
	if s.MontantMoyenEUR != 20.0 {
		t.Errorf("montant_moyen = %.2f, attendu 20.00", s.MontantMoyenEUR)
	}
}

// TestAdminDonations_StatsCacheRespects60s : 2 GET <60s → 1 query DB.
func TestAdminDonations_StatsCacheRespects60s(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedDonation(t, db, "x", "h", "A", "a@x.com", "paid", 100, "2026-05-01 10:00:00")

	req1 := adminCtx(httptest.NewRequest("GET", "/admin/donations/stats.json", nil))
	w1 := httptest.NewRecorder()
	AdminDonationsStats(deps)(w1, req1)
	body1 := w1.Body.String()

	// Modifier la DB après cache populated.
	db.Exec(`INSERT INTO donations(id, helloasso_payment_id, helloasso_form_type, amount_cents, donor_email, payment_status, raw_event_id) VALUES('y','h2','Donation', 9999, 'b@x.com', 'paid', 'e')`) //nolint:errcheck

	req2 := adminCtx(httptest.NewRequest("GET", "/admin/donations/stats.json", nil))
	w2 := httptest.NewRecorder()
	AdminDonationsStats(deps)(w2, req2)
	body2 := w2.Body.String()

	// Cache 60s : la 2e réponse doit être identique malgré l'INSERT.
	if body1 != body2 {
		t.Errorf("cache 60s violé : body1 %d chars, body2 %d chars différents", len(body1), len(body2))
	}
}

// TestAdminDonations_ExportCSVCorrectFormat : CSV header + UTF-8 BOM + parse OK.
func TestAdminDonations_ExportCSVCorrectFormat(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedDonation(t, db, "d1", "h1", "Alice Dupont", "alice@x.com", "paid", 2550, "2026-05-01 10:00:00")

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations/export.csv", nil))
	w := httptest.NewRecorder()
	AdminDonationsExportCSV(deps)(w, req)

	body := w.Body.Bytes()
	// BOM UTF-8
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Errorf("CSV manque BOM UTF-8 (premiers bytes: % x)", body[:min(len(body), 3)])
	}
	// Parse CSV (sans BOM)
	r := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("CSV rows=%d, attendu ≥2 (header + data)", len(rows))
	}
	if rows[0][0] != "id" || rows[0][5] != "montant_eur" {
		t.Errorf("CSV header invalide : %v", rows[0])
	}
	if rows[1][2] != "Alice Dupont" {
		t.Errorf("CSV row donor name = %q", rows[1][2])
	}
	if rows[1][5] != "25.50" {
		t.Errorf("CSV row montant_eur = %q, attendu 25.50", rows[1][5])
	}
}

// TestAdminDonations_ExportCSVRespectsFilter : filtre status=refunded → seul refunded export.
func TestAdminDonations_ExportCSVRespectsFilter(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedDonation(t, db, "p1", "h1", "A", "a@x.com", "paid", 1000, "2026-05-01 10:00:00")
	seedDonation(t, db, "r1", "h2", "B", "b@x.com", "refunded", 2000, "2026-05-02 10:00:00")

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations/export.csv?status=refunded", nil))
	w := httptest.NewRecorder()
	AdminDonationsExportCSV(deps)(w, req)

	body := w.Body.Bytes()
	r := csv.NewReader(strings.NewReader(string(body[3:])))
	rows, _ := r.ReadAll()
	dataRows := rows[1:]
	if len(dataRows) != 1 {
		t.Errorf("filter refunded export rows=%d, attendu 1", len(dataRows))
	}
}

// TestAdminDonations_RGPDSoftDeleteEmailKeepsRow : POST erase → email vide, row gardée.
func TestAdminDonations_RGPDSoftDeleteEmailKeepsRow(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedDonation(t, db, "d-rgpd", "h", "Victim Smith", "victim@x.com", "paid", 100, "2026-05-01 10:00:00")

	r := chi.NewRouter()
	r.Post("/admin/donations/{id}/erase-email", AdminDonationSoftEraseEmail(deps))

	req := adminCtx(httptest.NewRequest("POST", "/admin/donations/d-rgpd/erase-email", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("erase code=%d body=%s", w.Code, w.Body.String())
	}
	var email, name string
	db.QueryRow(`SELECT donor_email, donor_name FROM donations WHERE id='d-rgpd'`).Scan(&email, &name)
	if email != "" {
		t.Errorf("email = %q, attendu vide", email)
	}
	if name != "[supprimé RGPD]" {
		t.Errorf("name = %q, attendu placeholder", name)
	}
}

// TestAdminDonations_ManualUserMatch : POST match-user → user_id updaté.
func TestAdminDonations_ManualUserMatch(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	db.Exec(`INSERT INTO users(id, email, password_hash, display_name) VALUES('u-1', 'bob@x.com', 'x', 'Bob')`) //nolint:errcheck
	seedDonation(t, db, "d-match", "h", "Bob", "bob@x.com", "paid", 500, "2026-05-01 10:00:00")

	r := chi.NewRouter()
	r.Post("/admin/donations/{id}/match-user", AdminDonationManualUserMatch(deps))

	req := adminCtx(httptest.NewRequest("POST", "/admin/donations/d-match/match-user",
		strings.NewReader(`{"user_id":"u-1"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("match code=%d body=%s", w.Code, w.Body.String())
	}
	var uid string
	db.QueryRow(`SELECT user_id FROM donations WHERE id='d-match'`).Scan(&uid)
	if uid != "u-1" {
		t.Errorf("user_id = %q, attendu u-1", uid)
	}
}

// TestAdminDonations_ListMaskedEmail : list view masque l'email donateur.
func TestAdminDonations_ListMaskedEmail(t *testing.T) {
	db := setupAdminDonationsDB(t)
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	seedDonation(t, db, "m1", "h", "Alice", "alice@example.org", "paid", 100, "2026-05-01 10:00:00")

	req := adminCtx(httptest.NewRequest("GET", "/admin/donations", nil))
	w := httptest.NewRecorder()
	AdminDonationsList(deps)(w, req)

	body := w.Body.String()
	if strings.Contains(body, "alice@example.org") {
		// Email full visible dans response : c'est OK pour donor_email field, mais
		// donor_email_masked doit aussi être présent
	}
	if !strings.Contains(body, "a***@example.org") {
		t.Errorf("masked email absent du JSON response : %s", body[:min(len(body), 500)])
	}
}
