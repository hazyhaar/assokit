package listing_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/pkg/listing"
)

// siloPath retourne le chemin du fichier config/silos/yachts.toml relatif au test.
func siloPath(t *testing.T) string {
	t.Helper()
	// __file__ = pkg/listing/listing_test.go → remonte 2 niveaux → assokit root
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	return filepath.Join(root, "config", "silos", "yachts.toml")
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Activer les pragmas via Exec pour éviter les incompatibilités DSN modernc
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupStore(t *testing.T) *listing.Store {
	t.Helper()
	silo, err := listing.LoadSilo(siloPath(t))
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	db := openTestDB(t)
	s, err := listing.NewStore(context.Background(), db, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// fixtures A&C réelles — données modèles constructeurs + inventaire bateaux-antilles.fr
// ownerBroker est l'id vendeur partagé par les fixtures (un courtier réel
// gérant l'inventaire). Owner-scoping testé séparément avec un second owner.
const ownerBroker = "owner-bateaux-antilles"

var yachtsFixtures = []listing.Listing{
	{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "NEEL 43 - 2021",
		Body:       "Trimaran offshore NEEL-Trimarans, coque n°1328. Excellent état, Martinique. Voiles neuves 2022, moteur Volvo 40ch, pilote automatique Garmin.",
		PriceCents: 45000000, // 450 000 €
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "NEEL-Trimarans",
			"type_coque": "trimaran",
			"longueur_m": 13.1,
			"annee":      2021,
		},
	},
	{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "Dufour 500 Grand Large - 2014",
		Body:       "Monocoque hauturier Dufour Yachts, coque n°1342. Tour du monde possible, 5 cabines, Martinique. Réfit complet 2020, GPS chartplotter Raymarine.",
		PriceCents: 28000000, // 280 000 €
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "Dufour",
			"type_coque": "monocoque",
			"longueur_m": 15.05,
			"annee":      2014,
		},
	},
	{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "Jeanneau Sun Odyssey 440 - 2020",
		Body:       "Monocoque famille Jeanneau, coque n°1343. Martinique. Pack bluewater, 3 cabines, motorisation Yanmar 45ch. Parfait état.",
		PriceCents: 21500000, // 215 000 €
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "Jeanneau",
			"type_coque": "monocoque",
			"longueur_m": 13.39,
			"annee":      2020,
		},
	},
	{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "Excess 14 - 2024",
		Body:       "Catamaran sportif Excess, coque n°1369. Neuf de chantier, France. Design Finot-Conq, 4 cabines, moteurs électriques, panneaux solaires 2kW.",
		PriceCents: 38000000, // 380 000 €
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "Excess",
			"type_coque": "catamaran",
			"longueur_m": 13.73,
			"annee":      2024,
		},
	},
	{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "Jeanneau Sun Odyssey 519 - 2018",
		Body:       "Grand monocoque Jeanneau, 5 cabines, 3 salles de bain. Voilure en carbone, Martinique. Idéal navigation hauturière caraïbes.",
		PriceCents: 25000000, // 250 000 €
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "Jeanneau",
			"type_coque": "monocoque",
			"longueur_m": 15.55,
			"annee":      2018,
		},
	},
}

func insertFixtures(t *testing.T, s *listing.Store) {
	t.Helper()
	ctx := context.Background()
	for i := range yachtsFixtures {
		cp := yachtsFixtures[i]
		cp.ID = "" // laisser Create générer l'ID
		if err := s.Create(ctx, &cp); err != nil {
			t.Fatalf("insert fixture %d: %v", i, err)
		}
	}
}

// TestCreate vérifie l'insertion basique et l'attribution d'un ID.
func TestCreate(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	l := listing.Listing{
		Silo:       "yachts",
		OwnerID:    ownerBroker,
		Title:      "Bavaria 46 Cruiser - 2016",
		Body:       "Monocoque Bavaria, Martinique, 4 cabines.",
		PriceCents: 18000000,
		Status:     listing.StatusPublished,
		Attrs: map[string]any{
			"marque":     "Bavaria",
			"type_coque": "monocoque",
			"longueur_m": 14.27,
			"annee":      2016,
		},
	}
	if err := s.Create(ctx, &l); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if l.ID == "" {
		t.Fatal("ID non attribué")
	}

	results, err := s.Search(ctx, listing.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("attendu 1 résultat, obtenu %d", len(results))
	}
	if results[0].ID != l.ID {
		t.Fatalf("ID attendu %s, obtenu %s", l.ID, results[0].ID)
	}
}

// TestFacetFilter vérifie le filtrage exact par facette (marque=Jeanneau).
func TestFacetFilter(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	results, err := s.Search(ctx, listing.Filter{
		Facets: map[string]string{"marque": "Jeanneau"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Search facette: %v", err)
	}
	// Deux Jeanneau dans les fixtures
	if len(results) != 2 {
		t.Fatalf("attendu 2 Jeanneau, obtenu %d", len(results))
	}
	for _, r := range results {
		if r.Attrs["marque"] != "Jeanneau" {
			t.Errorf("résultat non-Jeanneau dans le filtre : %s", r.Title)
		}
	}
}

// TestRangeFilter vérifie le filtrage par longueur_m (BETWEEN 13.0 et 13.5).
func TestRangeFilter(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	minL, maxL := 13.0, 13.5
	results, err := s.Search(ctx, listing.Filter{
		Ranges: map[string]listing.RangeFilter{
			"longueur_m": {Min: &minL, Max: &maxL},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search range: %v", err)
	}
	// NEEL 43 (13.1) + Sun Odyssey 440 (13.39) → 2 résultats
	if len(results) != 2 {
		t.Fatalf("attendu 2 bateaux entre 13.0 et 13.5 m, obtenu %d", len(results))
	}
	for _, r := range results {
		l := r.Attrs["longueur_m"].(float64)
		if l < 13.0 || l > 13.5 {
			t.Errorf("longueur %f hors range [13.0, 13.5] pour %s", l, r.Title)
		}
	}
}

// TestFTS5Search vérifie la recherche texte (FTS5) sur le titre/body.
func TestFTS5Search(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	results, err := s.Search(ctx, listing.Filter{
		Text:  "trimaran",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search FTS5: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("attendu 1 résultat FTS5 'trimaran', obtenu %d", len(results))
	}
	if results[0].Attrs["type_coque"] != "trimaran" {
		t.Errorf("résultat inattendu : %s", results[0].Title)
	}
}

// TestFTS5EmptyQuery vérifie que Search avec texte vide retourne tous les listings du silo.
func TestFTS5EmptyQuery(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	results, err := s.Search(ctx, listing.Filter{Text: "", Limit: 20})
	if err != nil {
		t.Fatalf("Search vide: %v", err)
	}
	if len(results) != len(yachtsFixtures) {
		t.Fatalf("attendu %d résultats (tous), obtenu %d", len(yachtsFixtures), len(results))
	}
}

// TestFacetCounts vérifie les compteurs de facettes après insertions.
func TestFacetCounts(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	counts, err := s.FacetCounts(ctx, "marque")
	if err != nil {
		t.Fatalf("FacetCounts: %v", err)
	}
	if counts["Jeanneau"] != 2 {
		t.Errorf("attendu 2 Jeanneau, obtenu %d", counts["Jeanneau"])
	}
	if counts["NEEL-Trimarans"] != 1 {
		t.Errorf("attendu 1 NEEL-Trimarans, obtenu %d", counts["NEEL-Trimarans"])
	}
	if counts["Dufour"] != 1 {
		t.Errorf("attendu 1 Dufour, obtenu %d", counts["Dufour"])
	}
	if counts["Excess"] != 1 {
		t.Errorf("attendu 1 Excess, obtenu %d", counts["Excess"])
	}

	// type_coque : 3 monocoques, 1 trimaran, 1 catamaran
	coqueCounts, err := s.FacetCounts(ctx, "type_coque")
	if err != nil {
		t.Fatalf("FacetCounts type_coque: %v", err)
	}
	if coqueCounts["monocoque"] != 3 {
		t.Errorf("attendu 3 monocoques, obtenu %d", coqueCounts["monocoque"])
	}
}

// openBareDB ouvre une DB fichier avec un DSN MINIMAL : aucun busy_timeout,
// aucun cache partagé, mode rollback-journal par défaut. C'est le pire cas pour
// l'appelant — il sert à prouver que pkg/listing est robuste au SQLITE_BUSY
// INDÉPENDAMMENT du DSN injecté à la bordure (pilier core tenant-agnostic).
func openBareDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "listing.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open bare db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupBareStore(t *testing.T) *listing.Store {
	t.Helper()
	silo, err := listing.LoadSilo(siloPath(t))
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	db := openBareDB(t)
	s, err := listing.NewStore(context.Background(), db, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// TestConcurrentCreateNoBusy reproduit le bug de refreshFacetCounts : un curseur
// lecteur tenu ouvert pendant un upsert écrivain sur la même *sql.DB, aggravé par
// des Create concurrents. Sur un DSN minimal (sans busy_timeout) ce scénario
// déclenche SQLITE_BUSY sans le durcissement du store. Ce test est un garde
// permanent : il doit passer sous `go test -race`.
func TestConcurrentCreateNoBusy(t *testing.T) {
	s := setupBareStore(t)
	ctx := context.Background()

	const workers = 8
	const perWorker = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				cp := yachtsFixtures[i%len(yachtsFixtures)]
				cp.ID = ""
				if err := s.Create(ctx, &cp); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Create concurrent a échoué (SQLITE_BUSY attendu sans fix) : %v", err)
	}

	results, err := s.Search(ctx, listing.Filter{Limit: 1000})
	if err != nil {
		t.Fatalf("Search post-concurrence: %v", err)
	}
	if len(results) != workers*perWorker {
		t.Fatalf("attendu %d listings insérés, obtenu %d", workers*perWorker, len(results))
	}
}

// TestCombinedFacetAndRange vérifie la combinaison facette + range.
func TestCombinedFacetAndRange(t *testing.T) {
	s := setupStore(t)
	insertFixtures(t, s)
	ctx := context.Background()

	// monocoques de moins de 14 m → Sun Odyssey 440 (13.39 m) uniquement
	maxL := 14.0
	results, err := s.Search(ctx, listing.Filter{
		Facets: map[string]string{"type_coque": "monocoque"},
		Ranges: map[string]listing.RangeFilter{
			"longueur_m": {Max: &maxL},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search combiné: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("attendu 1 monocoque < 14m, obtenu %d", len(results))
	}
	if results[0].Attrs["marque"] != "Jeanneau" {
		t.Errorf("attendu Jeanneau Sun Odyssey 440, obtenu %s", results[0].Title)
	}
}
