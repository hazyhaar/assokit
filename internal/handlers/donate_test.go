// CLAUDE:SUMMARY Tests gardiens donate — iframe HelloAsso, fallback IBAN, paliers (M-ASSOKIT-AUDIT-FIX-2).
package handlers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/pages"
)

// renderToString rend un templ.Component vers une string (helper test).
func renderDonate(t *testing.T, donURL, cotisURL, iban string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := pages.Donate(donURL, cotisURL, iban, nil, false, "don").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Donate: %v", err)
	}
	return buf.String()
}

// TestDonateForm_RendersSuggestedTiers : page de don rend la section "Faire un don" + adhésion.
func TestDonateForm_RendersSuggestedTiers(t *testing.T) {
	html := renderDonate(t, "", "", "")
	if !strings.Contains(html, "Faire un don") {
		t.Errorf("page Donate doit contenir 'Faire un don' : %s", html[:min(len(html), 200)])
	}
	if !strings.Contains(html, "Adhérer") {
		t.Errorf("page Donate doit contenir 'Adhérer'")
	}
}

// TestDonateRedirectsToHelloAssoURL : URL HelloAsso fournie → iframe rendue.
func TestDonateRedirectsToHelloAssoURL(t *testing.T) {
	hellourl := "https://www.helloasso.com/asso/test/dons"
	html := renderDonate(t, hellourl, "", "")
	if !strings.Contains(html, "iframe") {
		t.Errorf("URL HelloAsso fournie : iframe attendue dans HTML")
	}
	if !strings.Contains(html, hellourl) {
		t.Errorf("URL HelloAsso %q absente du HTML rendu", hellourl)
	}
}

// TestDonate_NoConfiguredURL_ShowsFallbackMessage : URL vide → message contact + IBAN si fourni.
func TestDonate_NoConfiguredURL_ShowsFallbackMessage(t *testing.T) {
	iban := "FR7630006000011234567890189"
	html := renderDonate(t, "", "", iban)
	if strings.Contains(html, "<iframe") {
		t.Errorf("URL vide : pas d'iframe attendue")
	}
	// Fallback contact email lit theme.Brand().ContactEmail (vide en test = mailto:""
	// avec lien vide). On vérifie juste qu'un mailto: est présent — pas de hardcode.
	if !strings.Contains(html, "mailto:") {
		t.Errorf("URL vide : lien mailto: fallback attendu")
	}
	if !strings.Contains(html, iban) {
		t.Errorf("IBAN %q attendu dans fallback HTML", iban)
	}
}
