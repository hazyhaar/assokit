// CLAUDE:SUMMARY Tests : slugs de repli forum non-collisionnants (UUIDv7 complet) + garde anti-régression slug UUIDv7 tronqué dans internal/.
package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tree "github.com/hazyhaar/assokit/internal/nodetree"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// TestForumFallbackSlug_NoCollisionSameMillisecond prouve qu'une rafale de
// créations de nœuds forum à slug de repli généré d'affilée (même milliseconde)
// ne collisionne pas. Le préfixe UUIDv7 étant horodaté, une troncature `[:8]`
// rendait identiques tous les slugs émis dans la même milliseconde ; l'UUIDv7
// complet conserve la queue aléatoire et garantit l'unicité.
func TestForumFallbackSlug_NoCollisionSameMillisecond(t *testing.T) {
	db := newTestDB(t)
	treeStore := &tree.Store{DB: db}
	ctx := context.Background()

	rootID, err := treeStore.Create(ctx, tree.Node{Slug: "fallback-root", Type: "folder", Title: "Root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	const n = 16
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		// Reproduit les 3 sites de repli de forum.go : "reply-"/"topic-"/"question-"
		// + uid.New() COMPLET, émis sans pause (même milliseconde garantie).
		slug := "reply-" + uid.New()
		if _, ok := seen[slug]; ok {
			t.Fatalf("collision de slug de repli à l'itération %d : %q déjà émis", i, slug)
		}
		seen[slug] = struct{}{}
		if _, err := treeStore.Create(ctx, tree.Node{
			Slug:     slug,
			ParentID: newNullString(rootID),
			Type:     "post",
			Title:    "Reply",
		}); err != nil {
			t.Fatalf("create %d (slug=%q) doit réussir, got: %v", i, slug, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE slug LIKE 'reply-%'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Errorf("nœuds de repli créés = %d, want %d (aucune création perdue sur collision)", count, n)
	}
}

// TestNoTruncatedUIDForSlug est une garde PERMANENTE : elle échoue si un préfixe
// UUIDv7 tronqué réapparaît comme suffixe de slug dans internal/. Tronquer la
// sortie de uid.New par un slicing de préfixe ne garde que l'horodatage UUIDv7
// → collision en boucle de créations même-milliseconde (incident crash-loop
// niveau Branche, S1). Tout nouveau slug de repli doit utiliser uid.New() COMPLET.
func TestNoTruncatedUIDForSlug(t *testing.T) {
	// Aiguille construite par concaténation pour que CE fichier ne s'auto-matche pas.
	needle := "uid.New()" + "[:"

	// cwd du test = internal/handlers ; la racine internal/ est le parent.
	internalRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var offenders []string
	err = filepath.WalkDir(internalRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), needle) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internalRoot, err)
	}
	if len(offenders) != 0 {
		t.Errorf("UUIDv7 tronqué (%q) interdit comme suffixe de slug — collisionne même-milliseconde. Sites: %v", needle, offenders)
	}
}
