// CLAUDE:SUMMARY Tests gardiens donate — popup CSP-compatible, paliers, Mes dons, admin tools (M-ASSOKIT-SPRINT3-S3).
package handlers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
)

// renderDonate : helper compat pour tests pré-existants utilisant la signature simple.
func renderDonate(t *testing.T, donURL, cotisURL, iban string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := pages.Donate(donURL, cotisURL, iban, nil, false, "don").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Donate: %v", err)
	}
	return buf.String()
}

// renderDonateRich rend DonateRich avec props custom.
func renderDonateRich(t *testing.T, p pages.DonateProps) string {
	t.Helper()
	var buf bytes.Buffer
	if err := pages.DonateRich(p).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render DonateRich: %v", err)
	}
	return buf.String()
}

// TestDonateForm_RendersSuggestedTiers : page rend section "Faire un don" + adhésion.
func TestDonateForm_RendersSuggestedTiers(t *testing.T) {
	html := renderDonate(t, "", "", "")
	if !strings.Contains(html, "Faire un don") {
		t.Errorf("page Donate doit contenir 'Faire un don'")
	}
	if !strings.Contains(html, "Adhérer") {
		t.Errorf("page Donate doit contenir 'Adhérer'")
	}
}

// TestDonateRedirectsToHelloAssoURL : URL HelloAsso fournie → boutons palier rendus avec target=_blank.
func TestDonateRedirectsToHelloAssoURL(t *testing.T) {
	hellourl := "https://www.helloasso.com/asso/test/dons"
	html := renderDonateRich(t, pages.DonateProps{
		DonURL: hellourl, Paliers: []int{10, 30, 50},
	})
	if !strings.Contains(html, hellourl) {
		t.Errorf("URL HelloAsso %q absente du HTML", hellourl)
	}
	if !strings.Contains(html, `target="_blank"`) {
		t.Error("target=_blank attendu (fallback no-JS popup)")
	}
}

// TestDonate_NoConfiguredURL_ShowsFallbackMessage : URL vide → message contact + IBAN si fourni.
func TestDonate_NoConfiguredURL_ShowsFallbackMessage(t *testing.T) {
	iban := "FR7630006000011234567890189"
	html := renderDonate(t, "", "", iban)
	if strings.Contains(html, "<iframe") {
		t.Error("URL vide : pas d'iframe attendue")
	}
	if !strings.Contains(html, "mailto:") {
		t.Error("URL vide : lien mailto: fallback attendu")
	}
	if !strings.Contains(html, iban) {
		t.Errorf("IBAN %q attendu dans fallback HTML", iban)
	}
}

// TestDonatePage_NoIframeRendered : aucune balise <iframe> rendue (CSP-compatible).
func TestDonatePage_NoIframeRendered(t *testing.T) {
	html := renderDonateRich(t, pages.DonateProps{
		DonURL: "https://www.helloasso.com/x", Paliers: []int{10, 30, 50},
	})
	if strings.Contains(html, "<iframe") {
		t.Errorf("HTML contient <iframe (bloqué par CSP frame-src)")
	}
}

// TestDonatePage_PaliersFromBrandingKV : paliers passés rendent N boutons + montant libre.
func TestDonatePage_PaliersFromBrandingKV(t *testing.T) {
	html := renderDonateRich(t, pages.DonateProps{
		DonURL: "https://hello/x", Paliers: []int{10, 30, 50, 100},
	})
	for _, p := range []string{"10 €", "30 €", "50 €", "100 €", "Montant libre"} {
		if !strings.Contains(html, p) {
			t.Errorf("palier %q manquant dans HTML", p)
		}
	}
	if !strings.Contains(html, "noopener") {
		t.Error("rel=noopener manquant (tabnabbing risk)")
	}
	if !strings.Contains(html, "noreferrer") {
		t.Error("rel=noreferrer manquant")
	}
}

// TestDonatePage_FallbackMessageIfNoURLConfigured : URL vide → message "Soutien en cours de configuration".
func TestDonatePage_FallbackMessageIfNoURLConfigured(t *testing.T) {
	html := renderDonateRich(t, pages.DonateProps{DonURL: "", IBAN: "FR76..."})
	if !strings.Contains(html, "Soutien en cours de configuration") {
		t.Errorf("URL vide : message fallback attendu")
	}
	if strings.Contains(html, "donate-palier") {
		t.Error("URL vide : pas de boutons paliers attendus")
	}
}

// TestDonatePage_AuthenticatedShowsMyDonations : user + donations → bloc "Mes dons".
func TestDonatePage_AuthenticatedShowsMyDonations(t *testing.T) {
	user := &auth.User{ID: "u-1", Email: "u@x.com"}
	donations := []pages.MyDonationView{
		{Date: "01/01/2026", Amount: "25,00 €", FormType: "Donation", Status: "paid"},
		{Date: "15/02/2026", Amount: "50,00 €", FormType: "Donation", Status: "refunded"},
	}
	html := renderDonateRich(t, pages.DonateProps{
		DonURL: "https://hello/x", User: user, MyDonations: donations,
	})
	if !strings.Contains(html, "Mes dons") {
		t.Error("bloc Mes dons absent")
	}
	if !strings.Contains(html, "25,00 €") || !strings.Contains(html, "50,00 €") {
		t.Errorf("montants donations absents")
	}
	if !strings.Contains(html, "status-paid") || !strings.Contains(html, "status-refunded") {
		t.Error("status CSS classes absentes")
	}
}

// TestDonatePage_AuthenticatedNoDonationsShowsEmptyState : user sans donations → "Aucun don encore".
func TestDonatePage_AuthenticatedNoDonationsShowsEmptyState(t *testing.T) {
	user := &auth.User{ID: "u-2"}
	html := renderDonateRich(t, pages.DonateProps{User: user, DonURL: "https://hello"})
	if !strings.Contains(html, "Aucun don encore") {
		t.Error("empty state Mes dons absent")
	}
}

// TestDonatePage_AdminShowsConfigureLink : isAdmin → bouton "Configurer HelloAsso".
func TestDonatePage_AdminShowsConfigureLink(t *testing.T) {
	user := &auth.User{ID: "admin-1", Roles: []string{"admin"}}
	html := renderDonateRich(t, pages.DonateProps{User: user, IsAdmin: true, DonURL: "https://hello"})
	if !strings.Contains(html, "Configurer HelloAsso") {
		t.Error("bouton 'Configurer HelloAsso' absent (admin user)")
	}
	if !strings.Contains(html, "/admin/connectors/helloasso/configure") {
		t.Error("lien admin connector absent")
	}
}

// TestDonatePage_NonAdminHidesConfigureLink : non-admin → pas de bouton admin.
func TestDonatePage_NonAdminHidesConfigureLink(t *testing.T) {
	html := renderDonateRich(t, pages.DonateProps{
		User: &auth.User{ID: "u-x"}, IsAdmin: false, DonURL: "https://hello",
	})
	if strings.Contains(html, "Configurer HelloAsso") {
		t.Error("bouton admin visible pour non-admin")
	}
}

// TestDonatePage_AnonymousShowsButtonsNoMyDonations : visiteur anonyme → paliers, pas de Mes dons.
func TestDonatePage_AnonymousShowsButtonsNoMyDonations(t *testing.T) {
	html := renderDonateRich(t, pages.DonateProps{
		DonURL: "https://hello/x", Paliers: []int{10, 30}, User: nil,
	})
	if !strings.Contains(html, "donate-palier") {
		t.Error("boutons palier absents pour anonyme")
	}
	if strings.Contains(html, "Mes dons") {
		t.Error("bloc Mes dons visible pour anonyme")
	}
}
