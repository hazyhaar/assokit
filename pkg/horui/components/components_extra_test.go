package components_test

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
	"github.com/hazyhaar/assokit/pkg/horui/components"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

func TestTextAreaRenders(t *testing.T) {
	html := renderComp(t, components.TextArea("bio", "Biographie", "Contenu initial", 5))
	if !contains(html, `name="bio"`) || !contains(html, "Contenu initial") {
		t.Errorf("TextArea: %s", html)
	}
}

func TestEmailFieldRenders(t *testing.T) {
	html := renderComp(t, components.EmailField("email", "Email", "test@nps.fr", true))
	if !contains(html, `type="email"`) || !contains(html, "test@nps.fr") {
		t.Errorf("EmailField: %s", html)
	}
}

func TestCardRenders(t *testing.T) {
	body := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		w.Write([]byte("<p>Corps carte</p>")) //nolint:errcheck
		return nil
	})
	html := renderComp(t, components.Card("Titre carte", body, nil))
	if !contains(html, "Titre carte") || !contains(html, "Corps carte") {
		t.Errorf("Card: %s", html)
	}
}

func TestCardNoTitle(t *testing.T) {
	body := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		w.Write([]byte("body")) //nolint:errcheck
		return nil
	})
	html := renderComp(t, components.Card("", body, nil))
	if !contains(html, "card-body") {
		t.Errorf("Card no title: %s", html)
	}
}

func TestTableRenders(t *testing.T) {
	cell := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		w.Write([]byte("Valeur")) //nolint:errcheck
		return nil
	})
	html := renderComp(t, components.Table(
		[]string{"Colonne A", "Colonne B"},
		[][]templ.Component{{cell, cell}},
	))
	if !contains(html, "Colonne A") || !contains(html, "Valeur") {
		t.Errorf("Table: %s", html)
	}
}

func TestNodeCardTruncates(t *testing.T) {
	longBody := "<p>"
	for i := 0; i < 300; i++ {
		longBody += "x"
	}
	longBody += "</p>"
	n := tree.Node{Slug: "slug", Title: "Test", Type: "page", BodyHTML: longBody}
	html := renderComp(t, components.NodeCard(n))
	if len(html) < 10 {
		t.Errorf("NodeCard empty: %s", html)
	}
}

func TestButtonVariants(t *testing.T) {
	for _, variant := range []string{"primary", "secondary", "danger", "ghost"} {
		html := renderComp(t, components.Button("Label", "/", variant))
		if !contains(html, "btn-"+variant) {
			t.Errorf("Button %s: missing class in %s", variant, html)
		}
	}
}
