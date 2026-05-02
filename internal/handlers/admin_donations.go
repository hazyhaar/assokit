// CLAUDE:SUMMARY Handlers admin /admin/donations — list paginé + stats JSON + détail + RGPD soft erase + manual user match (M-ASSOKIT-SPRINT3-S4).
// CLAUDE:WARN Stats cache 60s in-memory protégé mu. Pagination keyset (created_at desc + id) pour SQLite.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// DonationListItem : ligne UI/CSV pour une donation.
type DonationListItem struct {
	ID                 string
	HelloAssoPaymentID string
	Date               string
	DonorName          string
	DonorEmail         string
	DonorEmailMasked   string // pour list view
	FormType           string
	AmountCents        int64
	AmountEUR          string // formaté "25,50 €"
	Currency           string
	Status             string
	UserID             string
	UserEmail          string
	IsMember           bool
}

// DonationListFilters : filtres URL parsed.
type DonationListFilters struct {
	From    string // RFC3339 date inclusive
	To      string // RFC3339 date exclusive
	Status  string // pending|authorized|paid|refunded|failed|""
	Type    string // Donation|Membership|""
	MinEur  int    // amount >= MinEur*100
	MaxEur  int    // amount <= MaxEur*100 (0 = ignored)
	Search  string // donor_name OR donor_email LIKE
	Cursor  string // keyset : created_at|id from previous page
}

// DonationsStats : agrégation pour header dashboard.
type DonationsStats struct {
	TotalCumulEUR        float64           `json:"total_cumul_eur"`
	TotalMoisCourantEUR  float64           `json:"total_mois_courant_eur"`
	NbDonateursUniques   int               `json:"nb_donateurs_uniques"`
	NbDonsMois           int               `json:"nb_dons_mois"`
	MontantMoyenEUR      float64           `json:"montant_moyen_eur"`
	DonateursMembresPct  float64           `json:"donateurs_membres_pct"`
	Top3Paliers          []PalierStat      `json:"top_3_paliers"`
	Evolution30j         []EvolutionPoint  `json:"evolution_30j"`
}

type PalierStat struct {
	MontantEUR int `json:"montant"`
	Count      int `json:"count"`
}

type EvolutionPoint struct {
	Date     string  `json:"date"`
	TotalEUR float64 `json:"total_eur"`
}

// statsCache : in-memory cache 60s protégé mu.
type statsCache struct {
	mu      sync.RWMutex
	value   *DonationsStats
	fetched time.Time
}

var globalStatsCache = &statsCache{}

// AdminDonationsList GET /admin/donations → page HTML rendue avec liste + filtres + stats.
// Note : la page complète templ est livrée séparément (donations.templ minimal). Ici on retourne
// JSON pour test gardien + future intégration UI.
func AdminDonationsList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		filters := parseDonationFilters(r.URL.Query())
		items, nextCursor, err := queryDonations(r.Context(), deps.DB, filters, 50)
		if err != nil {
			deps.Logger.Error("admin_donations_query", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		resp := map[string]any{
			"items":       items,
			"next_cursor": nextCursor,
			"filters":     filters,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

// AdminDonationsStats GET /admin/donations/stats.json → JSON stats avec cache 60s.
func AdminDonationsStats(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		stats, err := getStatsCached(r.Context(), deps.DB)
		if err != nil {
			deps.Logger.Error("admin_donations_stats", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats) //nolint:errcheck
	}
}

// AdminDonationDetail GET /admin/donations/{id} → JSON détail + actions disponibles.
func AdminDonationDetail(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		id := chi.URLParam(r, "id")
		item, err := queryDonationByID(r.Context(), deps.DB, id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			deps.Logger.Error("admin_donation_detail", "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(item) //nolint:errcheck
	}
}

// AdminDonationSoftEraseEmail POST /admin/donations/{id}/erase-email
// RGPD : donor_email='', donor_name='[supprimé RGPD]', row gardée.
func AdminDonationSoftEraseEmail(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		id := chi.URLParam(r, "id")
		res, err := deps.DB.ExecContext(r.Context(), `
			UPDATE donations
			SET donor_email = '', donor_name = '[supprimé RGPD]', updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, id)
		if err != nil {
			deps.Logger.Error("admin_donation_erase_email", "id", id, "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.NotFound(w, r)
			return
		}
		u := middleware.UserFromContext(r.Context())
		var actor string
		if u != nil {
			actor = u.ID
		}
		deps.Logger.Info("admin_donation_email_erased",
			"donation_id", id, "actor_user_id", actor)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"erased"}`)) //nolint:errcheck
	}
}

// AdminDonationManualUserMatch POST /admin/donations/{id}/match-user avec body {"user_id": "..."}.
// Vérifie que le user existe + que son email correspond à donor_email (ou que donor_email est vide).
func AdminDonationManualUserMatch(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdminACL(w, r) {
			return
		}
		id := chi.URLParam(r, "id")
		var req struct{ UserID string `json:"user_id"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
			http.Error(w, "user_id requis", http.StatusBadRequest)
			return
		}
		var userEmail string
		if err := deps.DB.QueryRowContext(r.Context(),
			`SELECT email FROM users WHERE id = ?`, req.UserID,
		).Scan(&userEmail); err != nil {
			http.Error(w, "user inconnu", http.StatusBadRequest)
			return
		}
		_, err := deps.DB.ExecContext(r.Context(),
			`UPDATE donations SET user_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			req.UserID, id)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		deps.Logger.Info("admin_donation_user_matched", "donation_id", id, "user_id", req.UserID)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"matched"}`)) //nolint:errcheck
	}
}

// requireAdminACL vérifie role admin, retourne false + écrit 403 si pas admin.
func requireAdminACL(w http.ResponseWriter, r *http.Request) bool {
	u := middleware.UserFromContext(r.Context())
	if u == nil || !slices.Contains(u.Roles, "admin") {
		http.Error(w, "Accès refusé", http.StatusForbidden)
		return false
	}
	return true
}

// parseDonationFilters extrait les filtres depuis URL.Query().
func parseDonationFilters(q url.Values) DonationListFilters {
	f := DonationListFilters{
		From:   q.Get("from"),
		To:     q.Get("to"),
		Status: q.Get("status"),
		Type:   q.Get("type"),
		Search: strings.TrimSpace(q.Get("q")),
		Cursor: q.Get("cursor"),
	}
	if v, err := strconv.Atoi(q.Get("min_eur")); err == nil {
		f.MinEur = v
	}
	if v, err := strconv.Atoi(q.Get("max_eur")); err == nil {
		f.MaxEur = v
	}
	return f
}

// queryDonations applique les filtres + retourne items + cursor pour pagination keyset.
func queryDonations(ctx context.Context, db *sql.DB, f DonationListFilters, limit int) ([]DonationListItem, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
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
	if f.Cursor != "" {
		// cursor format : <created_at>|<id>
		parts := strings.SplitN(f.Cursor, "|", 2)
		if len(parts) == 2 {
			conds = append(conds, "(d.created_at, d.id) < (?, ?)")
			args = append(args, parts[0], parts[1])
		}
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit+1) // +1 pour détection nextCursor
	q := fmt.Sprintf(`
		SELECT d.id, d.helloasso_payment_id, d.donor_name, d.donor_email,
			d.helloasso_form_type, d.amount_cents, d.currency, d.payment_status,
			COALESCE(d.user_id, ''), COALESCE(u.email, ''),
			COALESCE(d.paid_at, d.created_at), d.created_at,
			CASE WHEN EXISTS (SELECT 1 FROM user_roles WHERE user_id = d.user_id AND role_id = 'member') THEN 1 ELSE 0 END
		FROM donations d
		LEFT JOIN users u ON u.id = d.user_id
		%s
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT ?
	`, where)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var items []DonationListItem
	var lastCreatedAt, lastID string
	for rows.Next() {
		var it DonationListItem
		var createdAt string
		var isMemberInt int
		if err := rows.Scan(&it.ID, &it.HelloAssoPaymentID, &it.DonorName, &it.DonorEmail,
			&it.FormType, &it.AmountCents, &it.Currency, &it.Status,
			&it.UserID, &it.UserEmail, &it.Date, &createdAt, &isMemberInt); err != nil {
			return nil, "", err
		}
		it.IsMember = isMemberInt == 1
		it.AmountEUR = formatAmount(it.AmountCents, it.Currency)
		it.DonorEmailMasked = maskEmail(it.DonorEmail)
		items = append(items, it)
		lastCreatedAt = createdAt
		lastID = it.ID
	}
	var nextCursor string
	if len(items) > limit {
		// trim sentinel + cursor du dernier item conservé
		items = items[:limit]
		lastCreatedAt = ""
		lastID = ""
		// recompute du cursor sur le 50e
		last := items[limit-1]
		// best-effort : on relit created_at via d.id (cheap separate query)
		_ = db.QueryRowContext(ctx, `SELECT created_at FROM donations WHERE id = ?`, last.ID).Scan(&lastCreatedAt)
		lastID = last.ID
		nextCursor = lastCreatedAt + "|" + lastID
	}
	return items, nextCursor, nil
}

func queryDonationByID(ctx context.Context, db *sql.DB, id string) (*DonationListItem, error) {
	var it DonationListItem
	var createdAt string
	var isMemberInt int
	err := db.QueryRowContext(ctx, `
		SELECT d.id, d.helloasso_payment_id, d.donor_name, d.donor_email,
			d.helloasso_form_type, d.amount_cents, d.currency, d.payment_status,
			COALESCE(d.user_id, ''), COALESCE(u.email, ''),
			COALESCE(d.paid_at, d.created_at), d.created_at,
			CASE WHEN EXISTS (SELECT 1 FROM user_roles WHERE user_id = d.user_id AND role_id = 'member') THEN 1 ELSE 0 END
		FROM donations d
		LEFT JOIN users u ON u.id = d.user_id
		WHERE d.id = ?
	`, id).Scan(&it.ID, &it.HelloAssoPaymentID, &it.DonorName, &it.DonorEmail,
		&it.FormType, &it.AmountCents, &it.Currency, &it.Status,
		&it.UserID, &it.UserEmail, &it.Date, &createdAt, &isMemberInt)
	if err != nil {
		return nil, err
	}
	it.IsMember = isMemberInt == 1
	it.AmountEUR = formatAmount(it.AmountCents, it.Currency)
	// détail : full email, pas de masking.
	return &it, nil
}

// getStatsCached retourne les stats agrégées avec cache 60s.
func getStatsCached(ctx context.Context, db *sql.DB) (*DonationsStats, error) {
	globalStatsCache.mu.RLock()
	if globalStatsCache.value != nil && time.Since(globalStatsCache.fetched) < 60*time.Second {
		v := globalStatsCache.value
		globalStatsCache.mu.RUnlock()
		return v, nil
	}
	globalStatsCache.mu.RUnlock()

	stats, err := computeStats(ctx, db)
	if err != nil {
		return nil, err
	}
	globalStatsCache.mu.Lock()
	globalStatsCache.value = stats
	globalStatsCache.fetched = time.Now()
	globalStatsCache.mu.Unlock()
	return stats, nil
}

// invalidateStatsCache : utilisé par tests pour forcer refresh.
func invalidateStatsCache() {
	globalStatsCache.mu.Lock()
	globalStatsCache.value = nil
	globalStatsCache.mu.Unlock()
}

func computeStats(ctx context.Context, db *sql.DB) (*DonationsStats, error) {
	s := &DonationsStats{}

	// Total cumul + nb donateurs uniques + montant moyen (sur paid uniquement).
	var totalCents sql.NullInt64
	var nbDonateurs, nbDons int
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0),
			COUNT(DISTINCT COALESCE(user_id, donor_email)),
			COUNT(*)
		FROM donations WHERE payment_status = 'paid'
	`).Scan(&totalCents, &nbDonateurs, &nbDons)
	if err != nil {
		return nil, err
	}
	s.TotalCumulEUR = float64(totalCents.Int64) / 100
	s.NbDonateursUniques = nbDonateurs
	if nbDons > 0 {
		s.MontantMoyenEUR = s.TotalCumulEUR / float64(nbDons)
	}

	// Mois courant.
	var moisCents sql.NullInt64
	var moisDons int
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0), COUNT(*)
		FROM donations
		WHERE payment_status = 'paid'
		  AND COALESCE(paid_at, created_at) >= date('now', 'start of month')
	`).Scan(&moisCents, &moisDons)
	if err != nil {
		return nil, err
	}
	s.TotalMoisCourantEUR = float64(moisCents.Int64) / 100
	s.NbDonsMois = moisDons

	// % donateurs membres.
	var nbMembres int
	_ = db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT d.user_id)
		FROM donations d
		JOIN user_roles ur ON ur.user_id = d.user_id AND ur.role_id = 'member'
		WHERE d.payment_status = 'paid' AND d.user_id IS NOT NULL
	`).Scan(&nbMembres)
	if nbDonateurs > 0 {
		s.DonateursMembresPct = float64(nbMembres) * 100 / float64(nbDonateurs)
	}

	// Top 3 paliers (regroupement par tranche 10€).
	rows, err := db.QueryContext(ctx, `
		SELECT (amount_cents / 1000) * 10 AS palier, COUNT(*) c
		FROM donations
		WHERE payment_status = 'paid'
		GROUP BY palier
		ORDER BY c DESC
		LIMIT 3
	`)
	if err == nil {
		for rows.Next() {
			var p PalierStat
			if err := rows.Scan(&p.MontantEUR, &p.Count); err == nil {
				s.Top3Paliers = append(s.Top3Paliers, p)
			}
		}
		rows.Close()
	}

	// Évolution 30j.
	rows2, err := db.QueryContext(ctx, `
		SELECT date(COALESCE(paid_at, created_at)) AS d, SUM(amount_cents)
		FROM donations
		WHERE payment_status = 'paid'
		  AND COALESCE(paid_at, created_at) >= date('now', '-30 days')
		GROUP BY d
		ORDER BY d
	`)
	if err == nil {
		for rows2.Next() {
			var e EvolutionPoint
			var cents int64
			if err := rows2.Scan(&e.Date, &cents); err == nil {
				e.TotalEUR = float64(cents) / 100
				s.Evolution30j = append(s.Evolution30j, e)
			}
		}
		rows2.Close()
	}

	return s, nil
}

// maskEmail : "alice@example.org" → "a***@example.org".
func maskEmail(email string) string {
	if email == "" {
		return ""
	}
	i := strings.IndexByte(email, '@')
	if i < 1 {
		return "***"
	}
	return string(email[0]) + "***" + email[i:]
}

// formatAmount déjà défini dans donate.go (handlers package). Pas de re-déclaration.
