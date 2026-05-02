// CLAUDE:SUMMARY page_renderer — RenderPageFromBranding lit branding_kv ou fallback fichier .md (M-ASSOKIT-ADMIN-PANEL-V0.1).
// CLAUDE:WARN goldmark sans WithUnsafe : balises HTML brutes dans markdown sont escapées (anti-XSS).
package adminpanel

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/branding"
)

var _ = context.Background // keep import (utilisé par signature)

// markdownToHTML convertit du markdown en HTML safe (pas de WithUnsafe).
// Goldmark par défaut escape les balises HTML brutes dans le markdown.
func markdownToHTML(md string) (string, error) {
	var buf bytes.Buffer
	if err := goldmark.New().Convert([]byte(md), &buf); err != nil {
		return "", fmt.Errorf("goldmark: %w", err)
	}
	return buf.String(), nil
}

// PageData : données prêtes à rendre pour une page publique générée.
type PageData struct {
	Title    string
	BodyHTML string // HTML déjà escapé/safe
}

// RenderPageFromBranding retourne un handler qui rend une page publique
// depuis branding_kv (V0.1) avec fallback fichier .md (V0).
//
// slug = "charte" → lit branding_kv 'charte.body' (markdown), fallback BRANDING_DIR/pages/charte.md.
// slug = "mentions-legales" → lit legal.* keys, format en HTML structuré.
// slug = "a-propos" → lit quisommesnous.* keys.
// Si ni keys ni fichier → 404 explicite.
//
// renderFn permet au caller d'envelopper le content dans le layout (Base + theme).
func RenderPageFromBranding(deps app.AppDeps, slug string, brandingDir string, renderFn func(http.ResponseWriter, *http.Request, app.AppDeps, PageData)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := buildPageData(r.Context(), deps.DB, slug, brandingDir)
		if errors.Is(err, errPageNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			deps.Logger.Error("page_renderer", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur rendu page", http.StatusInternalServerError)
			return
		}
		renderFn(w, r, deps, page)
	}
}

var errPageNotFound = errors.New("page non trouvée (ni branding_kv ni fichier)")

// buildPageData construit le contenu HTML d'une page selon le slug.
func buildPageData(ctx context.Context, db *sql.DB, slug, brandingDir string) (PageData, error) {
	switch slug {
	case "charte":
		return buildCharte(ctx, db, brandingDir)
	case "mentions-legales":
		return buildMentionsLegales(ctx, db, brandingDir)
	case "a-propos":
		return buildAPropos(ctx, db, brandingDir)
	default:
		return PageData{}, errPageNotFound
	}
}

func buildCharte(ctx context.Context, db *sql.DB, brandingDir string) (PageData, error) {
	body := readKV(ctx, db, "charte.body")
	if body == "" {
		// Fallback fichier .md
		body = readBrandingFile(brandingDir, "charte.md")
	}
	if body == "" {
		return PageData{}, errPageNotFound
	}
	htmlBody, err := markdownToHTML(body)
	if err != nil {
		return PageData{}, err
	}
	return PageData{Title: "Charte / valeurs", BodyHTML: htmlBody}, nil
}

func buildMentionsLegales(ctx context.Context, db *sql.DB, brandingDir string) (PageData, error) {
	editeur := readKV(ctx, db, "legal.mentions_editeur")
	hebergeur := readKV(ctx, db, "legal.mentions_hebergeur")
	dpo := readKV(ctx, db, "legal.contact_dpo")
	statutsPath := readKV(ctx, db, "legal.statuts_pdf")
	rrPath := readKV(ctx, db, "legal.reglement_interieur_pdf")

	if editeur == "" && hebergeur == "" {
		// fallback fichier
		body := readBrandingFile(brandingDir, "mentions-legales.md")
		if body == "" {
			return PageData{}, errPageNotFound
		}
		htmlBody, err := markdownToHTML(body)
		if err != nil {
			return PageData{}, err
		}
		return PageData{Title: "Mentions légales", BodyHTML: htmlBody}, nil
	}

	var b strings.Builder
	if editeur != "" {
		b.WriteString(`<section><h2>Éditeur</h2><div>`)
		b.WriteString(html.EscapeString(editeur))
		b.WriteString(`</div></section>`)
	}
	if hebergeur != "" {
		b.WriteString(`<section><h2>Hébergeur</h2><p>`)
		b.WriteString(html.EscapeString(hebergeur))
		b.WriteString(`</p></section>`)
	}
	if dpo != "" {
		b.WriteString(`<section><h2>Contact RGPD</h2><p>`)
		b.WriteString(html.EscapeString(dpo))
		b.WriteString(`</p></section>`)
	}
	if statutsPath != "" {
		b.WriteString(`<p><a href="/static/uploads/`)
		b.WriteString(html.EscapeString(filepath.Base(statutsPath)))
		b.WriteString(`">Télécharger les statuts (PDF)</a></p>`)
	}
	if rrPath != "" {
		b.WriteString(`<p><a href="/static/uploads/`)
		b.WriteString(html.EscapeString(filepath.Base(rrPath)))
		b.WriteString(`">Télécharger le règlement intérieur (PDF)</a></p>`)
	}
	return PageData{Title: "Mentions légales", BodyHTML: b.String()}, nil
}

func buildAPropos(ctx context.Context, db *sql.DB, brandingDir string) (PageData, error) {
	histoire := readKV(ctx, db, "quisommesnous.histoire")
	if histoire == "" {
		body := readBrandingFile(brandingDir, "a-propos.md")
		if body == "" {
			return PageData{}, errPageNotFound
		}
		htmlBody, err := markdownToHTML(body)
		if err != nil {
			return PageData{}, err
		}
		return PageData{Title: "À propos", BodyHTML: htmlBody}, nil
	}

	var b strings.Builder
	b.WriteString(`<section><h2>Notre histoire</h2><p>`)
	b.WriteString(html.EscapeString(histoire))
	b.WriteString(`</p></section>`)

	// Missions
	missions := []string{
		readKV(ctx, db, "quisommesnous.mission_1"),
		readKV(ctx, db, "quisommesnous.mission_2"),
		readKV(ctx, db, "quisommesnous.mission_3"),
	}
	hasAny := false
	for _, m := range missions {
		if m != "" {
			hasAny = true
			break
		}
	}
	if hasAny {
		b.WriteString(`<section><h2>Nos missions</h2><ul>`)
		for _, m := range missions {
			if m != "" {
				b.WriteString(`<li>`)
				b.WriteString(html.EscapeString(m))
				b.WriteString(`</li>`)
			}
		}
		b.WriteString(`</ul></section>`)
	}

	// Bureau
	prez := readKV(ctx, db, "quisommesnous.president_bio")
	vp := readKV(ctx, db, "quisommesnous.bureau_vice_president")
	tres := readKV(ctx, db, "quisommesnous.bureau_tresorier")
	sec := readKV(ctx, db, "quisommesnous.bureau_secretaire")
	if prez != "" || vp != "" || tres != "" || sec != "" {
		b.WriteString(`<section><h2>Bureau</h2>`)
		if prez != "" {
			b.WriteString(`<div class="bio"><h3>Président</h3><p>`)
			b.WriteString(html.EscapeString(prez))
			b.WriteString(`</p></div>`)
		}
		b.WriteString(`<dl class="bureau-list">`)
		for _, kv := range []struct{ Label, Val string }{
			{"Vice-président", vp}, {"Trésorier", tres}, {"Secrétaire", sec},
		} {
			if kv.Val != "" {
				b.WriteString(`<dt>`)
				b.WriteString(kv.Label)
				b.WriteString(`</dt><dd>`)
				b.WriteString(html.EscapeString(kv.Val))
				b.WriteString(`</dd>`)
			}
		}
		b.WriteString(`</dl></section>`)
	}

	// Photo équipe
	photo := readKV(ctx, db, "quisommesnous.photo_equipe")
	if photo != "" {
		b.WriteString(`<section><img src="/static/uploads/`)
		b.WriteString(html.EscapeString(filepath.Base(photo)))
		b.WriteString(`" alt="Équipe" class="photo-equipe"/></section>`)
	}
	return PageData{Title: "À propos", BodyHTML: b.String()}, nil
}

// readKV lit branding_kv via le helper branding.Get (cache singleton).
func readKV(ctx context.Context, db *sql.DB, key string) string {
	if db == nil {
		return ""
	}
	return strings.TrimSpace(branding.Get(db, key))
}

// readBrandingFile lit BRANDING_DIR/pages/<file> si présent, sinon "".
func readBrandingFile(brandingDir, file string) string {
	if brandingDir == "" {
		return ""
	}
	path := filepath.Join(brandingDir, "pages", file)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
