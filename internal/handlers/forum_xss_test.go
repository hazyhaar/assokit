package handlers

import "testing"

// TestRenderForumMD_EchappeHTMLBrut garde le correctif XSS audité 2026-06-13 : le
// corps d'une réponse de forum est rendu en HTML SÛR (goldmark sans WithUnsafe),
// le HTML brut inséré par un membre est échappé. Sans cela, BodyHTML émis via
// @templ.Raw exécuterait le script chez chaque visiteur du fil.
func TestRenderForumMD_EchappeHTMLBrut(t *testing.T) {
	for _, payload := range []string{
		`<script>alert(document.cookie)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
	} {
		out := renderForumMD(payload)
		if contains(out, "<script") || contains(out, "onerror=") || contains(out, "javascript:") {
			t.Errorf("renderForumMD(%q) a laissé passer du HTML actif : %q", payload, out)
		}
	}
	// Le markdown légitime reste rendu.
	if got := renderForumMD("**gras**"); !contains(got, "<strong>") {
		t.Errorf("renderForumMD du markdown valide attendu rendu, got %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
