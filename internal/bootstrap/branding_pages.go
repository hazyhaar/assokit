package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	tree "github.com/hazyhaar/nodetree"
)

// SeedBrandingPages upsert idempotent des pages CMS depuis BrandingFS/pages/*.md.
// accueil.md est mappé sur le slug "home" (handler d'accueil). Tout autre <name>.md
// → slug <name>. Sans dossier pages/ : no-op.
//
// Corps stocké : si la première ligne non vide du markdown source est un « # Titre »
// dont le texte égale le titre dérivé (titleFromMarkdown), cette ligne est retirée
// du body_md — le titre reste porté par Node.Title et rendu en h1 par StaticPage.
//
// Idempotence (empreinte = markdown SOURCE du fichier, inchangé) :
//   - page absente → création avec corps normalisé ;
//   - page présente et body_md == corps normalisé attendu → rien ;
//   - page présente et body_md == source brut (ancien seed non édité) → re-seed
//     autorisé : migration vers le corps normalisé ;
//   - page présente et body_md différent de source et du corps normalisé →
//     préservation (priorité humain / admin).
func SeedBrandingPages(ctx context.Context, db *sql.DB, brandingFS fs.FS, logger *slog.Logger) error {
	if brandingFS == nil {
		return nil
	}
	pagesFS, err := fs.Sub(brandingFS, "pages")
	if err != nil {
		return nil
	}

	store := &tree.Store{DB: db}
	entries, err := fs.ReadDir(pagesFS, ".")
	if err != nil {
		return nil
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		bodyMD, err := fs.ReadFile(pagesFS, ent.Name())
		if err != nil {
			return fmt.Errorf("SeedBrandingPages read %s: %w", ent.Name(), err)
		}
		sourceMD := strings.TrimSpace(string(bodyMD))
		if sourceMD == "" {
			continue
		}

		slug := brandingPageSlug(ent.Name())
		title := titleFromMarkdown(sourceMD, slug)
		normalizedMD := brandingPageBodyMD(sourceMD, title)

		existing, err := store.GetBySlug(ctx, slug)
		if errors.Is(err, tree.ErrNotFound) {
			if _, err := store.Create(ctx, tree.Node{
				Slug:   slug,
				Type:   "page",
				Title:  title,
				BodyMD: normalizedMD,
			}); err != nil {
				return fmt.Errorf("SeedBrandingPages create %q: %w", slug, err)
			}
			if logger != nil {
				logger.Info("branding pages: seeded", "slug", slug, "file", ent.Name())
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("SeedBrandingPages get %q: %w", slug, err)
		}
		if existing.BodyMD == normalizedMD {
			continue
		}
		if existing.BodyMD == sourceMD {
			existing.Title = title
			existing.BodyMD = normalizedMD
			if err := store.Update(ctx, *existing); err != nil {
				return fmt.Errorf("SeedBrandingPages migrate %q: %w", slug, err)
			}
			if logger != nil {
				logger.Info("branding pages: migrated legacy seed", "slug", slug, "file", ent.Name())
			}
			continue
		}
		if logger != nil {
			logger.Debug("branding pages: skip (content differs from seed)", "slug", slug)
		}
	}
	return nil
}

func brandingPageSlug(filename string) string {
	base := strings.TrimSuffix(filename, path.Ext(filename))
	if base == "accueil" {
		return "home"
	}
	return base
}

func titleFromMarkdown(md, fallback string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		break
	}
	return fallback
}

// brandingPageBodyMD retourne le corps à stocker : retire la ligne « # Titre » initiale
// quand elle duplique le titre dérivé (évite un h1 en double au rendu StaticPage).
func brandingPageBodyMD(sourceMD, title string) string {
	lines := strings.Split(sourceMD, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			heading := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			if heading == title {
				return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			}
		}
		break
	}
	return sourceMD
}
