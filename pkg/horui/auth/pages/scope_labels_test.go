// CLAUDE:SUMMARY Tests gardiens scope_labels — coverage + fallback + no-jargon (M-ASSOKIT-DCR-4).
package pages

import (
	"strings"
	"testing"
)

// TestLibelleScope_KnownScopeReturnsLabel : scope mappé → libellé FR.
func TestLibelleScope_KnownScopeReturnsLabel(t *testing.T) {
	got := LibelleScope("feedback.create")
	if got != "Créer un nouveau feedback en votre nom" {
		t.Errorf("feedback.create = %q", got)
	}
}

// TestLibelleScope_UnknownScopeShowsFallback : scope non mappé → "Action technique : ..."
func TestLibelleScope_UnknownScopeShowsFallback(t *testing.T) {
	got := LibelleScope("unknown.scope.42")
	if !strings.Contains(got, "Action technique") {
		t.Errorf("fallback absent : %q", got)
	}
	if !strings.Contains(got, "unknown.scope.42") {
		t.Errorf("fallback ne cite pas le scope original : %q", got)
	}
	if got == "" {
		t.Error("fallback string vide (closed-world violé)")
	}
}

// TestLibellesScope_BatchMapping : []string scopes → []string libellés alignés.
func TestLibellesScope_BatchMapping(t *testing.T) {
	in := []string{"feedback.create", "forum.post.create", "profile.edit_self"}
	out := LibellesScope(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, attendu 3", len(out))
	}
	if !strings.Contains(out[0], "feedback") || !strings.Contains(out[1], "forum") {
		t.Errorf("mapping incorrect : %v", out)
	}
}

// TestScopeLabels_NoTechnicalJargon : aucun libellé FR ne contient jargon tech.
func TestScopeLabels_NoTechnicalJargon(t *testing.T) {
	banned := []string{"endpoint", "OAuth", "client_id", "JWT", "scope:", "API"}
	for scope, label := range ScopeLabelsFR {
		for _, b := range banned {
			// Permet "API" ou "scope" si dans phrase contextuelle (mais on exclut formulations strictes).
			if strings.Contains(label, b) {
				t.Errorf("scope %q libellé contient jargon technique %q : %q", scope, b, label)
			}
		}
	}
}

// TestScopeLabels_CritialActionsCovered : actions critiques de Boris ont un libellé.
// Si une action future est ajoutée au Registry sans libellé, le fallback "Action technique : X"
// est utilisé mais le test ici garantit la couverture des cas critiques V0.
func TestScopeLabels_CritialActionsCovered(t *testing.T) {
	critical := []string{
		"feedback.create", "feedback.list",
		"forum.post.create", "forum.post.delete",
		"users.list", "users.role_assign",
		"branding.set", "pages.update",
		"profile.edit_self", "account.delete_self",
		"donations.list",
	}
	for _, s := range critical {
		if _, ok := ScopeLabelsFR[s]; !ok {
			t.Errorf("scope critique %q non mappé en FR", s)
		}
	}
}

// TestScopeLabels_StandardOIDCScopes : openid, profile, email mappés FR.
func TestScopeLabels_StandardOIDCScopes(t *testing.T) {
	for _, s := range []string{"openid", "profile", "email"} {
		lib, ok := ScopeLabelsFR[s]
		if !ok {
			t.Errorf("OIDC standard scope %q non mappé", s)
		}
		if lib == "" {
			t.Errorf("scope %q libellé vide", s)
		}
	}
}
