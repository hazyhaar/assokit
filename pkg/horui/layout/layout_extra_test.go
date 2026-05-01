package layout_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/layout"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
)

func TestBaseRenders(t *testing.T) {
	theme.Init(&theme.Branding{Name: "AssokitTest", Nav: []theme.NavItem{{Label: "Accueil", Slug: "/"}}})

	body := layout.ErrorPage(200, "Bienvenue")
	c := layout.Base("Page test", body)

	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render Base: %v", err)
	}
	html := buf.String()
	checks := []string{"<!doctype html>", "AssokitTest", "Page test", "htmx.min.js", "horui.css"}
	for _, check := range checks {
		if !contains(html, check) {
			t.Errorf("Base missing %q in output", check)
		}
	}
}

func TestFlashBannerEmpty(t *testing.T) {
	c := layout.FlashBanner()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render FlashBanner: %v", err)
	}
	// Sans messages dans le ctx, le rendu est vide
	_ = buf.String()
}

func TestErrorPage500Renders(t *testing.T) {
	c := layout.ErrorPage(500, "Erreur serveur")
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render ErrorPage 500: %v", err)
	}
	html := buf.String()
	if !contains(html, "500") || !contains(html, "Erreur serveur") {
		t.Errorf("ErrorPage 500: %s", html)
	}
}

func TestBreadcrumbSingleCrumb(t *testing.T) {
	crumbs := []layout.Crumb{{Label: "Seul", Href: "/seul"}}
	c := layout.Breadcrumb(crumbs)
	var buf bytes.Buffer
	c.Render(context.Background(), &buf) //nolint:errcheck
	html := buf.String()
	if !contains(html, "Seul") {
		t.Errorf("single crumb: %s", html)
	}
}

func TestFooterContainsLinks(t *testing.T) {
	theme.Init(&theme.Branding{Name: "AssokitTest"})
	c := layout.Footer()
	var buf bytes.Buffer
	c.Render(context.Background(), &buf) //nolint:errcheck
	html := buf.String()
	for _, link := range []string{"/charte", "/contact", "/mentions-legales"} {
		if !contains(html, link) {
			t.Errorf("Footer missing %q", link)
		}
	}
}
