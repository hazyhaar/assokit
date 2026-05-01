package components_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
	"github.com/hazyhaar/assokit/pkg/horui/components"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

func renderComp(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestButtonRenders(t *testing.T) {
	html := renderComp(t, components.Button("Voir plus", "/more", "primary"))
	if !contains(html, "Voir plus") || !contains(html, "btn-primary") {
		t.Errorf("Button: %s", html)
	}
}

func TestBadgeRenders(t *testing.T) {
	html := renderComp(t, components.Badge("Nouveau", "success"))
	if !contains(html, "Nouveau") || !contains(html, "badge-success") {
		t.Errorf("Badge: %s", html)
	}
}

func TestSearchBarRenders(t *testing.T) {
	html := renderComp(t, components.SearchBar("/search", "Rechercher...", "query"))
	if !contains(html, `action="/search"`) || !contains(html, "query") {
		t.Errorf("SearchBar: %s", html)
	}
}

func TestModalRenders(t *testing.T) {
	content := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		w.Write([]byte("<p>Contenu modal</p>")) //nolint:errcheck
		return nil
	})
	html := renderComp(t, components.Modal("modal-1", "Titre modal", content))
	if !contains(html, "modal-1") || !contains(html, "Titre modal") {
		t.Errorf("Modal: %s", html)
	}
	if !contains(html, "<dialog") {
		t.Errorf("Modal should use dialog element: %s", html)
	}
}

func TestTextFieldRenders(t *testing.T) {
	html := renderComp(t, components.TextField("username", "Nom d'utilisateur", "", "ex: nom", true))
	if !contains(html, `name="username"`) || !contains(html, "required") {
		t.Errorf("TextField: %s", html)
	}
}

func TestPasswordFieldRenders(t *testing.T) {
	html := renderComp(t, components.PasswordField("password", "Mot de passe", true))
	if !contains(html, `type="password"`) {
		t.Errorf("PasswordField: %s", html)
	}
}

func TestSubmitButtonRenders(t *testing.T) {
	html := renderComp(t, components.SubmitButton("Valider"))
	if !contains(html, `type="submit"`) || !contains(html, "Valider") {
		t.Errorf("SubmitButton: %s", html)
	}
}

func TestSelectRenders(t *testing.T) {
	opts := []components.SelectOption{
		{Value: "fr", Label: "Français"},
		{Value: "en", Label: "English"},
	}
	html := renderComp(t, components.Select("lang", "Langue", opts, "fr"))
	if !contains(html, "Français") || !contains(html, "selected") {
		t.Errorf("Select: %s", html)
	}
}

func TestNodeCardRenders(t *testing.T) {
	n := tree.Node{
		ID:       "abc",
		Slug:     "test-page",
		Title:    "Ma page de test",
		Type:     "page",
		BodyHTML: "<p>Contenu HTML</p>",
		Depth:    1,
	}
	html := renderComp(t, components.NodeCard(n))
	if !contains(html, "Ma page de test") || !contains(html, "/n/test-page") {
		t.Errorf("NodeCard: %s", html)
	}
}

func TestTabsRenders(t *testing.T) {
	items := []components.TabItem{
		{Label: "Actualités", Href: "/news"},
		{Label: "Forum", Href: "/forum"},
	}
	html := renderComp(t, components.Tabs("Actualités", items))
	if !contains(html, "tab-active") || !contains(html, "Forum") {
		t.Errorf("Tabs: %s", html)
	}
}

func TestFormRenders(t *testing.T) {
	content := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		w.Write([]byte("<input type='text' name='x'/>")) //nolint:errcheck
		return nil
	})
	html := renderComp(t, components.Form("/submit", "POST", "csrf-token-test", content))
	if !contains(html, `action="/submit"`) || !contains(html, "csrf-token-test") {
		t.Errorf("Form: %s", html)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub)))
}
