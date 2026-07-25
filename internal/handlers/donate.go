// CLAUDE:SUMMARY Handler /soutenir (vague 2 : renderPageV2 + views.Donate).
package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/signupprofile"
)

// defaultPaliers : utilisé si branding_kv.helloasso.paliers_suggeres absent.
var defaultPaliers = []int{10, 30, 50, 100}

func handleDonatePage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		isMember := user != nil && slices.Contains(user.Roles, "member")
		isAdmin := user != nil && slices.Contains(user.Roles, "admin")

		paliers := loadPaliers(r.Context(), deps.DB)
		myDonations := []views.MyDonationView{}
		if user != nil {
			myDonations = loadMyDonations(r.Context(), deps.DB, user.ID)
		}

		renderPageV2(w, r, deps, "Soutenir",
			views.Donate(views.DonateProps{
				DonURL:      deps.Config.HelloassoDonURL,
				CotisURL:    deps.Config.HelloassoCotisationURL,
				IBAN:        deps.Config.BankIBAN,
				User:        user,
				IsMember:    isMember,
				IsAdmin:     isAdmin,
				AdhereSlug:  adhereSlug(deps.Profils),
				Paliers:     paliers,
				MyDonations: myDonations,
			}))
	}
}

// adhereSlug retourne l'ID du profil d'inscription à proposer sur /soutenir
// pour un visiteur non membre. Le catalogue de profils est injecté à la
// bordure (pas de slug "don" figé dans le core, qui n'existe pas forcément
// dans toutes les instances) : on retient le profil "visiteur" s'il existe,
// sinon le premier profil déclaré. Si le catalogue est vide, la chaîne vide
// fait retomber la vue sur le lien générique /participer.
func adhereSlug(profils []signupprofile.Profile) string {
	if p, ok := signupprofile.Find(profils, "visiteur"); ok {
		return p.ID
	}
	if len(profils) > 0 {
		return profils[0].ID
	}
	return ""
}

// loadPaliers lit branding_kv.helloasso.paliers_suggeres (CSV "10,30,50,100").
// Fallback defaultPaliers si vide ou parse fail.
func loadPaliers(ctx context.Context, db *sql.DB) []int {
	if db == nil {
		return defaultPaliers
	}
	var raw string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM branding_kv WHERE key = 'helloasso.paliers_suggeres'`,
	).Scan(&raw)
	if err != nil || raw == "" {
		return defaultPaliers
	}
	out := []int{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return defaultPaliers
	}
	return out
}

// loadMyDonations retourne les donations de userID (max 50, ordre paid_at desc).
func loadMyDonations(ctx context.Context, db *sql.DB, userID string) []views.MyDonationView {
	if db == nil || userID == "" {
		return nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT amount_cents, currency, helloasso_form_type, payment_status,
			COALESCE(paid_at, created_at)
		FROM donations
		WHERE user_id = ?
		ORDER BY COALESCE(paid_at, created_at) DESC
		LIMIT 50
	`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []views.MyDonationView
	for rows.Next() {
		var amount int64
		var currency, formType, status, when string
		if err := rows.Scan(&amount, &currency, &formType, &status, &when); err != nil {
			continue
		}
		out = append(out, views.MyDonationView{
			Date:     formatDate(when),
			Amount:   formatAmount(amount, currency),
			FormType: formType,
			Status:   status,
		})
	}
	return out
}

func formatAmount(cents int64, currency string) string {
	if currency == "" {
		currency = "EUR"
	}
	euros := cents / 100
	c := cents % 100
	sym := "€"
	if currency != "EUR" {
		sym = currency
	}
	return fmt.Sprintf("%d,%02d %s", euros, c, sym)
}

func formatDate(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("02/01/2006")
		}
	}
	return s
}
