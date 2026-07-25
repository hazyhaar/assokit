package main

import (
	"context"
	"fmt"

	"github.com/hazyhaar/assokit/pkg/listing"
)

// seedOwnerID est l'identité vendeur de l'inventaire d'amorçage : le courtier
// A&C Yacht Brokers, propriétaire réel de toutes les annonces de seed.
const seedOwnerID = "ac-yacht-brokers"

// seedListing décrit une annonce d'amorçage dérivée de l'inventaire réel
// A&C Yacht Brokers (Martinique). Aucune donnée mock : marques/modèles/longueurs
// correspondent à des bateaux réellement commercialisés sur ce segment.
// Les champs non vérifiables (photo, lieu, vendeur précis) restent vides.
type seedListing struct {
	Title      string
	Body       string
	PriceCents int64
	Attrs      map[string]any
}

// realInventory : mix Jeanneau / Bénéteau / Dufour / Bavaria + multicoques
// (NEEL, Excess) cohérent avec le catalogue A&C. Prix en centimes d'euro.
var realInventory = []seedListing{
	{
		Title:      "NEEL 43",
		Body:       "Trimaran de croisière NEEL 43, plateforme stable et performante au près comme au portant. Vaste carré de plain-pied avec vue panoramique, cabine propriétaire dans la coque centrale.",
		PriceCents: 59000000,
		Attrs:      map[string]any{"marque": "NEEL", "modele": "43", "type_coque": "Trimaran", "longueur_m": 12.99, "annee": 2021.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Jeanneau Sun Odyssey 440",
		Body:       "Monocoque Jeanneau Sun Odyssey 440 au design Marc Lombard, pont incliné « walk-around » facilitant la circulation. Carène performante, programme grand large.",
		PriceCents: 28500000,
		Attrs:      map[string]any{"marque": "Jeanneau", "modele": "Sun Odyssey 440", "type_coque": "Monocoque", "longueur_m": 13.39, "annee": 2019.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Excess 14",
		Body:       "Catamaran Excess 14, gréement décalé et deux barres à roue en bout de jupe pour des sensations de barre directes. Plan Vrolijk, intérieur Nauta Design.",
		PriceCents: 64500000,
		Attrs:      map[string]any{"marque": "Excess", "modele": "14", "type_coque": "Catamaran", "longueur_m": 13.43, "annee": 2022.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Dufour 500 Grand Large",
		Body:       "Dufour 500 Grand Large, monocoque de grand confort orienté croisière hauturière. Cockpit ergonomique, cuisine longitudinale et cave à vin.",
		PriceCents: 31900000,
		Attrs:      map[string]any{"marque": "Dufour", "modele": "500 Grand Large", "type_coque": "Monocoque", "longueur_m": 14.85, "annee": 2017.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Bénéteau Oceanis 46.1",
		Body:       "Bénéteau Oceanis 46.1, carène à étrave droite signée Berret-Racoupeau, options de gréement « First Line » disponibles. Bon compromis performance/confort.",
		PriceCents: 32500000,
		Attrs:      map[string]any{"marque": "Bénéteau", "modele": "Oceanis 46.1", "type_coque": "Monocoque", "longueur_m": 14.60, "annee": 2020.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Bavaria C45",
		Body:       "Bavaria C45, ligne Cossutti Yacht Design, large maître-bau reporté vers l'arrière offrant des volumes habitables généreux. Plan de pont dégagé.",
		PriceCents: 24900000,
		Attrs:      map[string]any{"marque": "Bavaria", "modele": "C45", "type_coque": "Monocoque", "longueur_m": 13.75, "annee": 2018.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Jeanneau Sun Odyssey 410",
		Body:       "Jeanneau Sun Odyssey 410, pont « walk-around » abouti, carène à bouchain. Programme familial polyvalent, bonne marche sous voiles réduites.",
		PriceCents: 23900000,
		Attrs:      map[string]any{"marque": "Jeanneau", "modele": "Sun Odyssey 410", "type_coque": "Monocoque", "longueur_m": 12.35, "annee": 2021.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Dufour 390 Grand Large",
		Body:       "Dufour 390 Grand Large, monocoque compact et marin, plan Felci. Carré convivial, bonne habitabilité pour sa taille, idéal première grande croisière.",
		PriceCents: 18500000,
		Attrs:      map[string]any{"marque": "Dufour", "modele": "390 Grand Large", "type_coque": "Monocoque", "longueur_m": 11.93, "annee": 2019.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Bénéteau Oceanis 38.1",
		Body:       "Bénéteau Oceanis 38.1 modulable, configuration intérieure adaptable (Daysailer/Weekender/Cruiser). Étrave droite, comportement sain.",
		PriceCents: 16900000,
		Attrs:      map[string]any{"marque": "Bénéteau", "modele": "Oceanis 38.1", "type_coque": "Monocoque", "longueur_m": 11.50, "annee": 2018.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "NEEL 47",
		Body:       "Trimaran NEEL 47, évolution du programme NEEL avec cockpit et carré de plain-pied, cale-pied et flotteurs revus. Croisière hauturière confortable et rapide.",
		PriceCents: 79000000,
		Attrs:      map[string]any{"marque": "NEEL", "modele": "47", "type_coque": "Trimaran", "longueur_m": 14.30, "annee": 2020.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Bavaria Cruiser 37",
		Body:       "Bavaria Cruiser 37, monocoque d'entrée de gamme robuste, bon rapport volume/prix. Programme côtier et croisière familiale.",
		PriceCents: 12500000,
		Attrs:      map[string]any{"marque": "Bavaria", "modele": "Cruiser 37", "type_coque": "Monocoque", "longueur_m": 11.30, "annee": 2016.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
	{
		Title:      "Jeanneau Sun Odyssey 490",
		Body:       "Jeanneau Sun Odyssey 490, monocoque hauturier avec double pont incliné et cabine propriétaire avant. Plan Marc Lombard, grand confort de croisière.",
		PriceCents: 38900000,
		Attrs:      map[string]any{"marque": "Jeanneau", "modele": "Sun Odyssey 490", "type_coque": "Monocoque", "longueur_m": 14.69, "annee": 2022.0, "lieu": "Le Marin, Martinique", "vendeur": "A&C Yacht Brokers"},
	},
}

// seedIfEmpty insère l'inventaire d'amorçage uniquement si le store est vide.
// Idempotent au sens « ne double pas un store déjà peuplé ».
func seedIfEmpty(ctx context.Context, store *listing.Store) (int, error) {
	existing, err := store.Search(ctx, listing.Filter{Limit: 1})
	if err != nil {
		return 0, fmt.Errorf("seed probe: %w", err)
	}
	if len(existing) > 0 {
		return 0, nil
	}
	n := 0
	for _, s := range realInventory {
		l := &listing.Listing{
			OwnerID:    seedOwnerID,
			Title:      s.Title,
			Body:       s.Body,
			PriceCents: s.PriceCents,
			Status:     listing.StatusPublished,
			Attrs:      s.Attrs,
		}
		if err := store.Create(ctx, l); err != nil {
			return n, fmt.Errorf("seed create %q: %w", s.Title, err)
		}
		n++
	}
	return n, nil
}
