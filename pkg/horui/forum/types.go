// CLAUDE:SUMMARY Forum viewmodel : ThreadNode + helpers (snippet, repliesLabel).
// Le ViewModel charge enfants jusqu'à MaxLoadDepth pour permettre récursion templ.
package forum

import (
	"context"
	"strings"

	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

// MaxLoadDepth : profondeur max chargée par BuildThread (au-delà, ChildCount conservé
// mais Children=nil → ThreadView affiche "Voir la suite du fil"). Aligné sur
// MaxRenderDepth pour cohérence — pas de lazy-load différé pour l'instant.
const MaxLoadDepth = MaxRenderDepth

// ThreadNode : nœud forum prêt à rendre, enfants chargés eagerly.
type ThreadNode struct {
	ID          string
	Slug        string
	Title       string
	BodyHTML    string
	SnippetHTML string
	AuthorName  string
	CreatedAt   string // formaté (Format("02/01/2006"))
	ChildCount  int
	Children    []ThreadNode
}

// BuildThread charge récursivement les enfants d'un nœud jusqu'à maxDepth.
// authorOf est appelé pour résoudre author_id → display_name (peut renvoyer ""
// si auteur supprimé/absent). Si nil, AuthorName reste vide.
func BuildThread(ctx context.Context, store *tree.Store, root tree.Node, authorOf func(ctx context.Context, userID string) string, maxDepth int) (ThreadNode, error) {
	tn := nodeToThread(root, authorOf, ctx)
	if maxDepth <= 0 {
		// Compter quand même les enfants pour le badge.
		kids, err := store.Children(ctx, root.ID)
		if err != nil {
			return tn, err
		}
		tn.ChildCount = len(kids)
		return tn, nil
	}
	kids, err := store.Children(ctx, root.ID)
	if err != nil {
		return tn, err
	}
	tn.ChildCount = len(kids)
	tn.Children = make([]ThreadNode, 0, len(kids))
	for _, k := range kids {
		child, err := BuildThread(ctx, store, k, authorOf, maxDepth-1)
		if err != nil {
			return tn, err
		}
		tn.Children = append(tn.Children, child)
	}
	return tn, nil
}

// BuildIndex charge les sujets racine d'un nœud parent (typiquement le nœud "forum"),
// sans descendre dans les fils — uniquement ChildCount sur chaque sujet.
func BuildIndex(ctx context.Context, store *tree.Store, parentID string, authorOf func(ctx context.Context, userID string) string) ([]ThreadNode, error) {
	kids, err := store.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]ThreadNode, 0, len(kids))
	for _, k := range kids {
		tn := nodeToThread(k, authorOf, ctx)
		grand, err := store.Children(ctx, k.ID)
		if err != nil {
			return nil, err
		}
		tn.ChildCount = len(grand)
		out = append(out, tn)
	}
	return out, nil
}

func nodeToThread(n tree.Node, authorOf func(ctx context.Context, userID string) string, ctx context.Context) ThreadNode {
	tn := ThreadNode{
		ID:        n.ID,
		Slug:      n.Slug,
		Title:     n.Title,
		BodyHTML:  n.BodyHTML,
		CreatedAt: n.CreatedAt.Format("02/01/2006"),
	}
	if n.BodyHTML != "" {
		tn.SnippetHTML = snippet(n.BodyHTML, 180)
	}
	if authorOf != nil && n.AuthorID.Valid {
		tn.AuthorName = authorOf(ctx, n.AuthorID.String)
	}
	return tn
}

// snippet : extrait HTML court pour les vignettes — strip balises grossier puis tronque.
func snippet(html string, max int) string {
	plain := stripTags(html)
	plain = strings.TrimSpace(plain)
	if len(plain) <= max {
		return plain
	}
	cut := plain[:max]
	if i := strings.LastIndexByte(cut, ' '); i > max-30 {
		cut = cut[:i]
	}
	return cut + "…"
}

func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func repliesLabel(n int) string {
	if n == 1 {
		return "réponse"
	}
	return "réponses"
}
