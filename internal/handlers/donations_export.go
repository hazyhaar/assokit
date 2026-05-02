// CLAUDE:SUMMARY Export CSV donations — UTF-8 BOM Excel-compatible, filtres respectés (M-ASSOKIT-SPRINT3-S4).
// CLAUDE:WARN Audit log dans slog avec actor + count, filtres URL = mêmes que liste.
package handlers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// AdminDonationsExportCSV GET /admin/donations/export.csv → CSV UTF-8 BOM.
func AdminDonationsExportCSV(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		filters := parseDonationFilters(r.URL.Query())
		// Pour export, on ignore le cursor (export = full dataset filtré).
		filters.Cursor = ""

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="donations-%s.csv"`, time.Now().UTC().Format("20060102-1504")))

		// UTF-8 BOM pour Excel France.
		w.Write([]byte{0xEF, 0xBB, 0xBF}) //nolint:errcheck

		writer := csv.NewWriter(w)
		defer writer.Flush()

		// Header.
		header := []string{
			"id", "date", "donateur", "email", "type",
			"montant_eur", "currency", "status",
			"user_id", "user_email", "is_member", "helloasso_payment_id",
		}
		if err := writer.Write(header); err != nil {
			deps.Logger.Error("export_csv_header", "err", err.Error())
			return
		}

		count, err := writeFilteredCSVRows(r.Context(), deps.DB, filters, writer)
		if err != nil {
			deps.Logger.Error("export_csv_rows", "err", err.Error())
			return
		}

		u := middleware.UserFromContext(r.Context())
		var actor string
		if u != nil {
			actor = u.ID
		}
		deps.Logger.Info("admin_donations_csv_exported",
			"actor_user_id", actor,
			"rows_count", count,
			"filters_status", filters.Status,
			"filters_type", filters.Type,
			"filters_from", filters.From,
			"filters_to", filters.To,
		)
	}
}

// writeFilteredCSVRows applique les mêmes filtres que la liste, mais sans pagination
// (full dataset, ordre paid_at desc).
func writeFilteredCSVRows(ctx context.Context, db *sql.DB, f DonationListFilters, w *csv.Writer) (int, error) {
	var conds []string
	var args []any
	if f.From != "" {
		conds = append(conds, "COALESCE(d.paid_at, d.created_at) >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		conds = append(conds, "COALESCE(d.paid_at, d.created_at) < ?")
		args = append(args, f.To)
	}
	if f.Status != "" {
		conds = append(conds, "d.payment_status = ?")
		args = append(args, f.Status)
	}
	if f.Type != "" {
		conds = append(conds, "d.helloasso_form_type = ?")
		args = append(args, f.Type)
	}
	if f.MinEur > 0 {
		conds = append(conds, "d.amount_cents >= ?")
		args = append(args, f.MinEur*100)
	}
	if f.MaxEur > 0 {
		conds = append(conds, "d.amount_cents <= ?")
		args = append(args, f.MaxEur*100)
	}
	if f.Search != "" {
		conds = append(conds, "(LOWER(d.donor_name) LIKE ? OR LOWER(d.donor_email) LIKE ?)")
		s := "%" + strings.ToLower(f.Search) + "%"
		args = append(args, s, s)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	q := fmt.Sprintf(`
		SELECT d.id, COALESCE(d.paid_at, d.created_at) AS date,
			d.donor_name, d.donor_email, d.helloasso_form_type,
			d.amount_cents, d.currency, d.payment_status,
			COALESCE(d.user_id, ''), COALESCE(u.email, ''),
			CASE WHEN EXISTS (SELECT 1 FROM user_roles WHERE user_id = d.user_id AND role_id = 'member') THEN 1 ELSE 0 END,
			d.helloasso_payment_id
		FROM donations d
		LEFT JOIN users u ON u.id = d.user_id
		%s
		ORDER BY date DESC
	`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, date, donorName, donorEmail, formType, currency, status, userID, userEmail, helloID string
		var amountCents int64
		var isMember int
		if err := rows.Scan(&id, &date, &donorName, &donorEmail, &formType,
			&amountCents, &currency, &status, &userID, &userEmail, &isMember, &helloID); err != nil {
			return count, err
		}
		amountEur := strconv.FormatFloat(float64(amountCents)/100, 'f', 2, 64)
		row := []string{
			id, date, donorName, donorEmail, formType,
			amountEur, currency, status,
			userID, userEmail, strconv.Itoa(isMember), helloID,
		}
		if err := w.Write(row); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
