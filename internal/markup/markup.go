// Package markup expose un rendu Markdown→HTML partagé (GFM, sauts de ligne durs).
// Extrait de pkg/messaging pour éviter la duplication entre messaging et salon.
// Aucune option WithUnsafe : le HTML généré est sûr à injecter dans une page.
package markup

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// Render convertit du Markdown en HTML sûr (pas de WithUnsafe).
// En cas d'erreur interne goldmark, retourne src inchangé.
func Render(src string) string {
	var b strings.Builder
	if err := md.Convert([]byte(src), &b); err != nil {
		return src
	}
	return b.String()
}
