// Package theme gère le branding runtime de l'instance assokit.
//
// Le branding est chargé une fois au boot depuis un branding.toml et stocké
// dans un singleton atomic.Value. Les helpers T() et Brand() sont thread-safe
// et lock-free en lecture (hot-path web).
//
// Reload runtime supporté : ReloadFromFS() peut être appelé depuis l'admin UI
// (Sprint 2) pour appliquer un nouveau branding sans redémarrer.
package theme

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/pelletier/go-toml/v2"
)

// Branding contient toute la configuration cosmétique et textuelle d'une instance.
type Branding struct {
	// Identité
	Name         string `toml:"name"`
	Tagline      string `toml:"tagline"`
	BaseURL      string `toml:"base_url"`
	ContactEmail string `toml:"contact_email"`

	// Couleurs (CSS hex, ex: "#1a73e8")
	Colors struct {
		Primary   string `toml:"primary"`
		Secondary string `toml:"secondary"`
		Accent    string `toml:"accent"`
		BgLight   string `toml:"bg_light"`
		BgDark    string `toml:"bg_dark"`
		Text      string `toml:"text"`
	} `toml:"colors"`

	// Typographie
	FontFamily string `toml:"font_family"`

	// Assets (chemins relatifs au BrandingFS racine)
	LogoPath    string `toml:"logo_path"`
	FaviconPath string `toml:"favicon_path"`
	OGImagePath string `toml:"og_image_path"`

	// Footer
	Footer struct {
		Lines []string `toml:"lines"`
	} `toml:"footer"`

	// Navigation top (slug → label)
	Nav []NavItem `toml:"nav"`

	// Textes UI surchargeables (i18n). key → string.
	Texts map[string]string `toml:"texts"`

	// Locale (code BCP 47, ex: "fr-FR"). Utilisé pour formatages futurs.
	Locale string `toml:"locale"`
}

// NavItem = entrée de menu top.
type NavItem struct {
	Slug  string `toml:"slug"`
	Label string `toml:"label"`
	Order int    `toml:"order"`
}

// brand stocke le pointeur courant *Branding. atomic.Value pour lecture lock-free.
var brand atomic.Value // *Branding

var (
	hexColorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
	invalidFontRegex = regexp.MustCompile(`[<>;}]`)
)

// Init est appelé une fois au boot avec le branding initial.
// Panique si b est nil.
func Init(b *Branding) {
	if b == nil {
		panic("theme.Init: nil branding")
	}
	brand.Store(b)
}

// Brand renvoie le branding courant. Toujours non-nil après Init.
// Si appelé avant Init, renvoie un Branding zéro avec textes vides.
func Brand() *Branding {
	if v := brand.Load(); v != nil {
		return v.(*Branding)
	}
	return &Branding{Texts: map[string]string{}, Locale: "fr"}
}

// T renvoie le texte UI pour la clé donnée, ou fallback si absent ou vide.
// Thread-safe, lock-free.
func T(key, fallback string) string {
	b := Brand()
	if v, ok := b.Texts[key]; ok && v != "" {
		return v
	}
	return fallback
}

// LoadFromFS charge un branding.toml depuis fsys.
func LoadFromFS(fsys fs.FS, path string) (*Branding, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var b Branding
	if err := toml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}

	if b.Name == "" {
		return nil, fmt.Errorf("validation error: name cannot be empty")
	}
	if b.BaseURL == "" {
		return nil, fmt.Errorf("validation error: base_url cannot be empty")
	}

	if b.Texts == nil {
		b.Texts = make(map[string]string)
	}

	// Validate colors
	validateColor := func(c *string, fallback string) {
		if *c == "" || !hexColorRegex.MatchString(*c) {
			*c = fallback
		}
	}
	validateColor(&b.Colors.Primary, "#1a73e8")
	validateColor(&b.Colors.Secondary, "#34a853")
	validateColor(&b.Colors.Accent, "#ea4335")
	validateColor(&b.Colors.BgLight, "#ffffff")
	validateColor(&b.Colors.BgDark, "#202124")
	validateColor(&b.Colors.Text, "#202124")

	if b.FontFamily == "" || invalidFontRegex.MatchString(b.FontFamily) {
		b.FontFamily = "Inter, system-ui, sans-serif"
	}

	// Sort nav items
	for i := 0; i < len(b.Nav); i++ {
		for j := i + 1; j < len(b.Nav); j++ {
			if b.Nav[i].Order > b.Nav[j].Order {
				b.Nav[i], b.Nav[j] = b.Nav[j], b.Nav[i]
			}
		}
	}

	return &b, nil
}

// ReloadFromFS recharge le branding depuis fsys et met à jour le singleton.
func ReloadFromFS(fsys fs.FS, path string) error {
	b, err := LoadFromFS(fsys, path)
	if err != nil {
		return err
	}
	Init(b)
	return nil
}

// CSSVars génère le bloc <style>:root { ... }</style> pour injection.
func CSSVars() string {
	b := Brand()
	return fmt.Sprintf(`<style>:root{--primary:%s;--secondary:%s;--accent:%s;--bg-light:%s;--bg-dark:%s;--text:%s;--font:%s;}</style>`,
		b.Colors.Primary, b.Colors.Secondary, b.Colors.Accent,
		b.Colors.BgLight, b.Colors.BgDark, b.Colors.Text,
		strings.ReplaceAll(b.FontFamily, `"`, `'`))
}
