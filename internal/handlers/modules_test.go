// CLAUDE:SUMMARY Tests de l'interrupteur d'activation des modules d'espace membre :
// validation fail-loud des slugs, résolution href→module, filtrage des cartes
// d'espace membre et des liens du tableau de bord quand un module est désactivé.
package handlers

import (
	"errors"
	"testing"

	"github.com/hazyhaar/assokit/internal/webui/views"
)

// TestValidateDisabledModules_KnownSlugOK : un slug reconnu passe la validation.
func TestValidateDisabledModules_KnownSlugOK(t *testing.T) {
	t.Parallel()
	if err := ValidateDisabledModules([]string{"conventions"}); err != nil {
		t.Fatalf("slug reconnu rejeté: %v", err)
	}
	if err := ValidateDisabledModules(nil); err != nil {
		t.Fatalf("liste vide rejetée: %v", err)
	}
}

// TestValidateDisabledModules_UnknownSlugFails : un slug inconnu est fail-loud.
func TestValidateDisabledModules_UnknownSlugFails(t *testing.T) {
	t.Parallel()
	err := ValidateDisabledModules([]string{"conventions", "inexistant"})
	if err == nil {
		t.Fatal("slug inconnu accepté, attendu une erreur")
	}
	var ume *UnknownModuleError
	if !errors.As(err, &ume) {
		t.Fatalf("type d'erreur = %T, attendu *UnknownModuleError", err)
	}
	if ume.Slug != "inexistant" {
		t.Errorf("slug fautif = %q, want inexistant", ume.Slug)
	}
}

// TestModuleForHref : la vue membre des conventions est rattachée à son module ;
// une route voisine ne l'est pas.
func TestModuleForHref(t *testing.T) {
	t.Parallel()
	if got := moduleForHref("/account/conventions"); got != "conventions" {
		t.Errorf("moduleForHref(/account/conventions) = %q, want conventions", got)
	}
	if got := moduleForHref("/account/parcelles"); got != "parcelles" {
		t.Errorf("moduleForHref(/account/parcelles) = %q, want parcelles", got)
	}
	// Route d'espace membre sans module désactivable rattaché.
	if got := moduleForHref("/account/membership"); got != "" {
		t.Errorf("moduleForHref(/account/membership) = %q, want \"\"", got)
	}
}

// TestFilterDisabledModuleCards : la carte « Mes conventions » disparaît quand le
// module est désactivé, et reste présente sinon (rétro-compat).
func TestFilterDisabledModuleCards(t *testing.T) {
	t.Parallel()
	cards := []views.AccountCardView{
		{Title: "Mes parcelles", Href: "/account/parcelles"},
		{Title: "Mes conventions", Href: "/account/conventions"},
	}

	// Aucun module désactivé → liste inchangée.
	if got := filterDisabledModuleCards(cards, nil); len(got) != 2 {
		t.Fatalf("sans désactivation: %d cartes, want 2", len(got))
	}

	// Conventions désactivé → carte retirée.
	got := filterDisabledModuleCards(cards, map[string]bool{"conventions": true})
	if len(got) != 1 {
		t.Fatalf("conventions off: %d cartes, want 1", len(got))
	}
	if got[0].Href == "/account/conventions" {
		t.Error("la carte du module désactivé est toujours rendue")
	}
}

// TestModuleDisabled : href sans module rattaché reste toujours actif.
func TestModuleDisabled(t *testing.T) {
	t.Parallel()
	disabled := map[string]bool{"conventions": true}
	if !moduleDisabled(disabled, "/account/conventions") {
		t.Error("module désactivé signalé actif")
	}
	if moduleDisabled(disabled, "/account/membership") {
		t.Error("route sans module signalée désactivée")
	}
	if !moduleDisabled(map[string]bool{"parcelles": true}, "/account/parcelles") {
		t.Error("module parcelles désactivé signalé actif")
	}
	if moduleDisabled(nil, "/account/conventions") {
		t.Error("aucune désactivation mais href signalé désactivé")
	}
}
