package listing_test

import (
	"context"
	"testing"

	"github.com/hazyhaar/assokit/pkg/listing"
)

func fptr(v float64) *float64 { return &v }

// seed crée un listing publié et retourne son id.
func seed(t *testing.T, s *listing.Store, owner, title, body string, price int64, attrs map[string]any) string {
	t.Helper()
	l := &listing.Listing{
		OwnerID:    owner,
		Title:      title,
		Body:       body,
		PriceCents: price,
		Status:     listing.StatusPublished,
		Attrs:      attrs,
	}
	if err := s.Create(context.Background(), l); err != nil {
		t.Fatalf("seed %q: %v", title, err)
	}
	return l.ID
}

func ids(ls []listing.Listing) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.ID
	}
	return out
}

// C1 : le tri est honoré pour recent/price_asc/price_desc/relevance ; défaut = recent.
func TestC1_Sort(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	// Trois listings, prix croissants ; créés dans l'ordre cheap→mid→pricey, donc
	// "recent" (created_at DESC) doit rendre pricey, mid, cheap.
	cheap := seed(t, s, "o", "Petit voilier coque bleue", "occasion", 1000, map[string]any{"marque": "A"})
	mid := seed(t, s, "o", "Voilier moyen", "occasion", 5000, map[string]any{"marque": "B"})
	pricey := seed(t, s, "o", "Grand yacht de luxe", "neuf", 9000, map[string]any{"marque": "C"})

	cases := []struct {
		name string
		sort listing.Sort
		text string
		want []string
	}{
		{"default_recent", "", "", []string{pricey, mid, cheap}},
		{"recent", listing.SortRecent, "", []string{pricey, mid, cheap}},
		{"price_asc", listing.SortPriceAsc, "", []string{cheap, mid, pricey}},
		{"price_desc", listing.SortPriceDesc, "", []string{pricey, mid, cheap}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := s.Search(ctx, listing.Filter{Sort: tc.sort, Text: tc.text})
			if err != nil {
				t.Fatal(err)
			}
			got := ids(res)
			if len(got) != len(tc.want) {
				t.Fatalf("%s: %d résultats, attendu %d (%v)", tc.name, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("%s: ordre %v, attendu %v", tc.name, got, tc.want)
				}
			}
		})
	}

	// relevance : "voilier" matche cheap+mid ; ordre par rank FTS, tous présents,
	// le grand yacht (sans "voilier") absent.
	res, err := s.Search(ctx, listing.Filter{Sort: listing.SortRelevance, Text: "voilier"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, l := range res {
		got[l.ID] = true
	}
	if !got[cheap] || !got[mid] {
		t.Fatalf("relevance: cheap+mid attendus, obtenu %v", ids(res))
	}
	if got[pricey] {
		t.Fatalf("relevance: 'grand yacht' (sans 'voilier') ne devrait pas matcher")
	}
}

// C2 : favoris Add/Remove/List par user, idempotent, cloisonné par user.
func TestC2_Favorites(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	l1 := seed(t, s, "o", "A", "x", 100, nil)
	l2 := seed(t, s, "o", "B", "x", 200, nil)

	if err := s.AddFavorite(ctx, "alice", l1); err != nil {
		t.Fatal(err)
	}
	// Idempotence : double Add ne duplique pas.
	if err := s.AddFavorite(ctx, "alice", l1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFavorite(ctx, "alice", l2); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFavorite(ctx, "bob", l2); err != nil {
		t.Fatal(err)
	}

	favA, err := s.ListFavorites(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(favA) != 2 {
		t.Fatalf("alice: 2 favoris attendus, %d (%v)", len(favA), ids(favA))
	}
	favB, err := s.ListFavorites(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(favB) != 1 || favB[0].ID != l2 {
		t.Fatalf("bob: doit voir seulement l2, %v", ids(favB))
	}

	// Remove idempotent + cloisonnement (remove par alice n'affecte pas bob).
	if err := s.RemoveFavorite(ctx, "alice", l1); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFavorite(ctx, "alice", l1); err != nil {
		t.Fatal(err)
	}
	favA, _ = s.ListFavorites(ctx, "alice")
	if len(favA) != 1 || favA[0].ID != l2 {
		t.Fatalf("alice après remove: doit voir l2, %v", ids(favA))
	}
	favB, _ = s.ListFavorites(ctx, "bob")
	if len(favB) != 1 {
		t.Fatalf("bob ne doit pas être affecté par le remove d'alice, %v", ids(favB))
	}
}

// C3 : MatchingSavedSearches — un listing matche une recherche dont les critères
// collent, et N'EST PAS renvoyé pour une recherche non-matchante (anti-faux-positif).
func TestC3_MatchingSavedSearches(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Recherche A : trimarans NEEL (facette marque + texte) → doit matcher.
	ssMatch, err := s.SaveSearch(ctx, "alice", "trimarans neel", listing.Filter{
		Facets: map[string]string{"marque": "NEEL-Trimarans"},
		Text:   "trimaran",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Recherche B : monocoques Dufour → NE DOIT PAS matcher un NEEL.
	ssNoMatch, err := s.SaveSearch(ctx, "bob", "dufour", listing.Filter{
		Facets: map[string]string{"marque": "Dufour"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Recherche C : range prix qui exclut (max 100k €) → NE DOIT PAS matcher (450k €).
	ssRange, err := s.SaveSearch(ctx, "carol", "petit budget", listing.Filter{
		Ranges: map[string]listing.RangeFilter{"price": {Max: fptr(10000000)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Recherche D : range longueur englobante (12-14m) → DOIT matcher (13.1m).
	ssRangeOK, err := s.SaveSearch(ctx, "dave", "13m", listing.Filter{
		Ranges: map[string]listing.RangeFilter{"longueur_m": {Min: fptr(12), Max: fptr(14)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	l := &listing.Listing{
		Silo:       "yachts",
		OwnerID:    "broker",
		Title:      "NEEL 43 - 2021",
		Body:       "Trimaran offshore NEEL-Trimarans",
		PriceCents: 45000000,
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "NEEL-Trimarans",
			"type_coque": "trimaran",
			"longueur_m": 13.1,
			"annee":      2021,
		},
	}

	matched, err := s.MatchingSavedSearches(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ss := range matched {
		got[ss.ID] = true
	}
	if !got[ssMatch.ID] {
		t.Errorf("recherche trimaran NEEL aurait dû matcher")
	}
	if !got[ssRangeOK.ID] {
		t.Errorf("range longueur 12-14m aurait dû matcher (13.1m)")
	}
	if got[ssNoMatch.ID] {
		t.Errorf("FAUX POSITIF: recherche Dufour matche un NEEL")
	}
	if got[ssRange.ID] {
		t.Errorf("FAUX POSITIF: range prix <100k matche un listing à 450k")
	}
}
