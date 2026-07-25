package corpus

import (
	"context"
	"testing"
)

func TestIsInert(t *testing.T) {
	if !IsInert(nil) {
		t.Fatal("nil provider doit être inerte")
	}
	if !IsInert(InertProvider{}) {
		t.Fatal("InertProvider doit être inerte")
	}
	if IsInert(&stubActiveProvider{}) {
		t.Fatal("stub actif ne doit pas être inerte")
	}
}

type stubActiveProvider struct{}

func (stubActiveProvider) SearchCorpus(context.Context, string, int) ([]Hit, error) {
	return nil, nil
}

// TestInertProvider_Vide vérifie l'état vide assumé : le fournisseur inerte ne
// fabrique aucun résultat, quelle que soit la requête.
func TestInertProvider_Vide(t *testing.T) {
	hits, err := InertProvider{}.SearchCorpus(context.Background(), "bail à ferme pastoral", 10)
	if err != nil {
		t.Fatalf("SearchCorpus inerte: erreur inattendue %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("fournisseur inerte : 0 résultat attendu, obtenu %d", len(hits))
	}
	if hits == nil {
		t.Fatal("fournisseur inerte : slice non-nil attendue (état vide explicite)")
	}
}

// TestInertProvider_SatisfaitProvider vérifie que InertProvider satisfait bien
// l'interface Provider (contrat côté consommateur).
func TestInertProvider_SatisfaitProvider(t *testing.T) {
	var _ Provider = InertProvider{}
}

// TestHit_Mapping documente la forme du Hit : un mapping d'adaptateur de bordure
// doit pouvoir remplir tous les champs sans dépendance externe.
func TestHit_Mapping(t *testing.T) {
	h := Hit{Content: "Article L135-3", Score: 0.87, Ref: "afp_droit_chunk_000142", Source: "LEGIARTI000022219346"}
	if h.Content == "" || h.Ref == "" {
		t.Fatalf("Hit mal formé: %+v", h)
	}
	if h.Score != 0.87 {
		t.Fatalf("score non préservé: %v", h.Score)
	}
}
