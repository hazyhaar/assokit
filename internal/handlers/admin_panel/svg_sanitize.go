package adminpanel

import (
	"strings"

	"golang.org/x/net/html"
)

var svgSkipElements = map[string]bool{
	"script":          true,
	"foreignobject":   true,
	"foreignObject":   true,
	"iframe":          true,
	"object":          true,
	"embed":           true,
}

// dangerousAttrsPrefixes : préfixes d'attributs à retirer (handlers JavaScript).
var dangerousAttrsPrefixes = []string{
	"on",
}

// sanitizeSVG parse un SVG, retire les éléments et attributs dangereux
// (<script>, <foreignObject>, on* handlers, javascript: URLs), puis
// sérialise le résultat. Si le parse échoue, retourne l'entrée inchangée
// (la défense Content-Disposition:attachment du serveur reste active).
func sanitizeSVG(content []byte) []byte {
	doc, err := html.Parse(strings.NewReader(string(content)))
	if err != nil {
		return content
	}
	walkSVG(doc)
	var sb strings.Builder
	if err := html.Render(&sb, doc); err != nil {
		return content
	}
	return []byte(sb.String())
}

// walkSVG parcourt récursivement le DOM et supprime les nœuds dangereux.
func walkSVG(n *html.Node) {
	if n.Type == html.ElementNode {
		if svgSkipElements[n.Data] {
			n.Parent.RemoveChild(n)
			return
		}
		cleanAttrs(n)
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		walkSVG(c)
		c = next
	}
}

func cleanAttrs(n *html.Node) {
	var kept []html.Attribute
	for _, attr := range n.Attr {
		name := strings.ToLower(attr.Key)
		if strings.HasPrefix(name, "on") {
			continue
		}
		if attr.Namespace == "http://www.w3.org/2000/svg" && name == "href" || name == "xlink:href" {
			val := strings.ToLower(strings.TrimSpace(attr.Val))
			if strings.HasPrefix(val, "javascript:") || strings.HasPrefix(val, "data:text/html") {
				continue
			}
		}
		if strings.EqualFold(attr.Val, "javascript:expression(0)") || strings.EqualFold(attr.Val, "javascript:expression(1)") {
			continue
		}
		kept = append(kept, attr)
	}
	n.Attr = kept
}
