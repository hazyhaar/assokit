package layout_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/layout"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

func render(t *testing.T, component interface{ Render(context.Context, *bytes.Buffer) error }) string {
	t.Helper()
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func renderWriter(t *testing.T, component interface {
	Render(context.Context, interface{ Write([]byte) (int, error) }) error
}) string {
	t.Helper()
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestBreadcrumbRenders(t *testing.T) {
	crumbs := []layout.Crumb{
		{Label: "Accueil", Href: "/"},
		{Label: "Forum", Href: "/forum"},
		{Label: "Page courante", Href: "/forum/page"},
	}
	c := layout.Breadcrumb(crumbs)
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Breadcrumb: %v", err)
	}
	html := buf.String()
	if !contains(html, "Accueil") || !contains(html, "Forum") {
		t.Errorf("breadcrumb should contain items, got: %s", html)
	}
	if !contains(html, "aria-current") {
		t.Errorf("last crumb should have aria-current")
	}
}

func TestBreadcrumbEmpty(t *testing.T) {
	c := layout.Breadcrumb(nil)
	var buf bytes.Buffer
	c.Render(context.Background(), &buf) //nolint:errcheck
	if buf.Len() > 0 {
		t.Logf("empty breadcrumb renders: %q (ok)", buf.String())
	}
}

func TestErrorPageRenders(t *testing.T) {
	c := layout.ErrorPage(404, "Page introuvable")
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render ErrorPage: %v", err)
	}
	html := buf.String()
	if !contains(html, "404") {
		t.Errorf("ErrorPage should contain code 404, got: %s", html)
	}
	if !contains(html, "Page introuvable") {
		t.Errorf("ErrorPage should contain message, got: %s", html)
	}
}

func TestHeaderRenders(t *testing.T) {
	th := theme.Defaults()
	th.SiteName = "TestSite"
	nav := []layout.NavItem{{Label: "Accueil", Href: "/"}, {Label: "Forum", Href: "/forum"}}
	c := layout.Header(th, nav)
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Header: %v", err)
	}
	html := buf.String()
	if !contains(html, "TestSite") {
		t.Errorf("Header should contain site name, got: %s", html)
	}
	if !contains(html, "Forum") {
		t.Errorf("Header should contain nav items, got: %s", html)
	}
}

func TestFooterRenders(t *testing.T) {
	th := theme.Defaults()
	c := layout.Footer(th)
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Footer: %v", err)
	}
	html := buf.String()
	if !contains(html, "Assokit") && !contains(html, "footer") {
		t.Errorf("Footer should contain footer content, got: %s", html)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub)))
}
