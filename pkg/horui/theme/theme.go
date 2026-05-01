// CLAUDE:SUMMARY Theme générique assokit — palette + fonts par défaut, persistance via table horui_config.
package theme

import (
	"database/sql"
	"fmt"
	"strings"
)

// Theme représente la charte graphique du site.
type Theme struct {
	PrimaryColor     string // couleur principale (anis)
	SecondaryColor   string // couleur secondaire (rouge)
	BackgroundColor  string // fond page
	SurfaceColor     string // fond cartes / sections
	TextColor        string // texte principal
	TextMutedColor   string // texte secondaire
	BorderColor      string // bordures
	AccentColor      string // accent (anis clair)
	FontFamily       string // police corps
	FontFamilyLabel  string // police labels / nav
	FontFamilyTitle  string // police titres
	BorderRadius     string // rayon bordure boutons/cartes
	LogoURL          string // URL logo (optionnel)
	SiteName         string // nom du site
	SiteTagline      string // accroche
}

// Defaults retourne la charte sober defaults — anis + rouge sur fond clair.
// Palette anis + rouge, typographie DM Sans / Bebas Neue / Barlow Condensed.
func Defaults() Theme {
	return Theme{
		PrimaryColor:    "#00897b", // --anis
		SecondaryColor:  "#d63031", // --red
		BackgroundColor: "#ffffff", // --white
		SurfaceColor:    "#f7f6f3", // --off
		TextColor:       "#1a1714", // --ink
		TextMutedColor:  "#6b6560", // --muted
		BorderColor:     "#e2ddd6", // --border
		AccentColor:     "#e0f2f1", // --anis-light
		FontFamily:      "'DM Sans', sans-serif",
		FontFamilyLabel: "'Barlow Condensed', sans-serif",
		FontFamilyTitle: "'Bebas Neue', sans-serif",
		BorderRadius:    "2px",
		SiteName:        "Mon Asso",
		SiteTagline:     "Plateforme citoyenne indépendante",
	}
}

// CSSVars génère le bloc :root { ... } injectable dans <style>.
func (t Theme) CSSVars() string {
	var b strings.Builder
	b.WriteString(":root {\n")
	fmt.Fprintf(&b, "  --color-primary: %s;\n", t.PrimaryColor)
	fmt.Fprintf(&b, "  --color-secondary: %s;\n", t.SecondaryColor)
	fmt.Fprintf(&b, "  --color-bg: %s;\n", t.BackgroundColor)
	fmt.Fprintf(&b, "  --color-surface: %s;\n", t.SurfaceColor)
	fmt.Fprintf(&b, "  --color-text: %s;\n", t.TextColor)
	fmt.Fprintf(&b, "  --color-muted: %s;\n", t.TextMutedColor)
	fmt.Fprintf(&b, "  --color-border: %s;\n", t.BorderColor)
	fmt.Fprintf(&b, "  --color-accent: %s;\n", t.AccentColor)
	fmt.Fprintf(&b, "  --font-body: %s;\n", t.FontFamily)
	fmt.Fprintf(&b, "  --font-label: %s;\n", t.FontFamilyLabel)
	fmt.Fprintf(&b, "  --font-title: %s;\n", t.FontFamilyTitle)
	fmt.Fprintf(&b, "  --radius: %s;\n", t.BorderRadius)
	b.WriteString("}\n")
	return b.String()
}

var themeKeys = []struct {
	key   string
	get   func(*Theme) *string
}{
	{"primary_color", func(t *Theme) *string { return &t.PrimaryColor }},
	{"secondary_color", func(t *Theme) *string { return &t.SecondaryColor }},
	{"background_color", func(t *Theme) *string { return &t.BackgroundColor }},
	{"surface_color", func(t *Theme) *string { return &t.SurfaceColor }},
	{"text_color", func(t *Theme) *string { return &t.TextColor }},
	{"text_muted_color", func(t *Theme) *string { return &t.TextMutedColor }},
	{"border_color", func(t *Theme) *string { return &t.BorderColor }},
	{"accent_color", func(t *Theme) *string { return &t.AccentColor }},
	{"font_family", func(t *Theme) *string { return &t.FontFamily }},
	{"font_family_label", func(t *Theme) *string { return &t.FontFamilyLabel }},
	{"font_family_title", func(t *Theme) *string { return &t.FontFamilyTitle }},
	{"border_radius", func(t *Theme) *string { return &t.BorderRadius }},
	{"logo_url", func(t *Theme) *string { return &t.LogoURL }},
	{"site_name", func(t *Theme) *string { return &t.SiteName }},
	{"site_tagline", func(t *Theme) *string { return &t.SiteTagline }},
}

// Load charge le thème depuis horui_config. Retourne Defaults() si la table est vide.
func Load(db *sql.DB) (Theme, error) {
	t := Defaults()
	rows, err := db.Query(`SELECT key, value FROM horui_config`)
	if err != nil {
		return t, fmt.Errorf("theme.Load query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return t, err
		}
		for _, entry := range themeKeys {
			if entry.key == k {
				*entry.get(&t) = v
				break
			}
		}
	}
	return t, rows.Err()
}

// Save upsert les valeurs du thème dans horui_config.
func Save(db *sql.DB, t Theme) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("theme.Save begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, entry := range themeKeys {
		_, err := tx.Exec(
			`INSERT INTO horui_config(key, value) VALUES(?, ?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			entry.key, *entry.get(&t),
		)
		if err != nil {
			return fmt.Errorf("theme.Save upsert %s: %w", entry.key, err)
		}
	}
	return tx.Commit()
}
