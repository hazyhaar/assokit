package main

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/webui"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/listing"
)

const defaultPageSize = 12

// yachtsHandlers porte le store du silo et la définition du vertical.
type yachtsHandlers struct {
	store *listing.Store
	silo  *listing.SiloDef
}

// rangeLabels mappe l'attribut de range vers libellé + unité d'affichage.
var rangeLabels = map[string][2]string{
	"price":      {"Prix (€)", "€"},
	"longueur_m": {"Longueur (m)", "m"},
	"annee":      {"Année", ""},
}

// facetLabels mappe l'attribut facettable vers son libellé.
var facetLabels = map[string]string{
	"marque":     "Marque",
	"type_coque": "Type de coque",
	"modele":     "Modèle",
}

func facetLabel(attr string) string {
	if l, ok := facetLabels[attr]; ok {
		return l
	}
	return attr
}

// buildIndexData lit la requête, interroge le store et compose IndexData.
// Pour offrir tri global + total + pagination, on récupère une fenêtre large
// filtrée puis on trie/pagine en Go (échelle inventaire vertical).
func (h *yachtsHandlers) buildIndexData(r *http.Request) (views.IndexData, error) {
	ctx := r.Context()
	q := r.URL.Query()

	filter := listing.Filter{
		Facets: map[string]string{},
		Ranges: map[string]listing.RangeFilter{},
		Text:   q.Get("q"),
		Limit:  10000, // fenêtre large : tri + total côté Go
	}

	// Facettes textuelles déclarées par le silo.
	d := views.IndexData{
		SiloLabel: h.silo.Label,
		SiloDesc:  h.silo.Description,
		Query:     q.Get("q"),
		Sort:      sortOrDefault(q.Get("sort")),
		Limit:     defaultPageSize,
	}

	for _, a := range h.silo.Attrs {
		if a.Kind != "text" {
			continue
		}
		sel := q.Get(a.Name)
		if sel != "" {
			filter.Facets[a.Name] = sel
		}
		counts, err := h.store.FacetCounts(ctx, a.Name)
		if err != nil {
			return d, err
		}
		fg := views.FacetGroup{Attr: a.Name, Label: facetLabel(a.Name), Selected: sel}
		for val, cnt := range counts {
			fg.Values = append(fg.Values, views.FacetValue{Value: val, Count: cnt})
		}
		sort.Slice(fg.Values, func(i, j int) bool { return fg.Values[i].Value < fg.Values[j].Value })
		d.Facets = append(d.Facets, fg)
	}

	// Ranges déclarés par le silo.
	for _, rd := range h.silo.Ranges {
		minS := q.Get(rd.Attr + "_min")
		maxS := q.Get(rd.Attr + "_max")
		rf := listing.RangeFilter{}
		if minS != "" {
			if v, err := strconv.ParseFloat(minS, 64); err == nil {
				if rd.Attr == "price" {
					v *= 100 // l'utilisateur saisit des euros, le store compare des centimes
				}
				rf.Min = &v
			}
		}
		if maxS != "" {
			if v, err := strconv.ParseFloat(maxS, 64); err == nil {
				if rd.Attr == "price" {
					v *= 100
				}
				rf.Max = &v
			}
		}
		if rf.Min != nil || rf.Max != nil {
			filter.Ranges[rd.Attr] = rf
		}
		lbl := rangeLabels[rd.Attr]
		d.Ranges = append(d.Ranges, views.RangeInput{Attr: rd.Attr, Label: lbl[0], Unit: lbl[1], Min: minS, Max: maxS})
	}

	all, err := h.store.Search(ctx, filter)
	if err != nil {
		return d, err
	}
	sortListings(all, d.Sort)

	d.Total = len(all)
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 || offset >= len(all) {
		offset = 0
	}
	d.Offset = offset
	end := offset + d.Limit
	if end > len(all) {
		end = len(all)
	}
	d.Listings = all[offset:end]
	return d, nil
}

func (h *yachtsHandlers) handleIndex(w http.ResponseWriter, r *http.Request) {
	d, err := h.buildIndexData(r)
	if err != nil {
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
		return
	}
	render(w, r, h.silo.Label, views.ListingIndexPage(d))
}

// handleFragment : même données, mais rend seulement le fragment de résultats
// (cible HTMX). Pas de shell.
func (h *yachtsHandlers) handleFragment(w http.ResponseWriter, r *http.Request) {
	d, err := h.buildIndexData(r)
	if err != nil {
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := views.ListingResults(d).Render(r.Context(), w); err != nil {
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}

func (h *yachtsHandlers) handleDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	results, err := h.store.Search(r.Context(), listing.Filter{Limit: 10000})
	if err != nil {
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
		return
	}
	var found *listing.Listing
	for i := range results {
		if results[i].ID == id {
			found = &results[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "Annonce introuvable", http.StatusNotFound)
		return
	}
	render(w, r, found.Title, views.ListingDetailPage(views.DetailData{
		Listing: *found,
		Specs:   buildSpecs(h.silo, *found),
	}))
}

// buildSpecs déroule toutes les attrs déclarées par le silo en lignes ordonnées.
func buildSpecs(silo *listing.SiloDef, l listing.Listing) []views.SpecRow {
	specLabels := map[string]string{
		"marque": "Marque", "modele": "Modèle", "type_coque": "Type de coque",
		"longueur_m": "Longueur", "annee": "Année", "lieu": "Lieu", "vendeur": "Vendeur",
	}
	order := []string{"marque", "modele", "type_coque", "longueur_m", "annee", "lieu"}
	var rows []views.SpecRow
	for _, k := range order {
		if v, ok := l.Attrs[k]; ok && v != nil {
			rows = append(rows, views.SpecRow{Label: specLabels[k], Value: views.AttrDisplay(l, k)})
		}
	}
	return rows
}

func sortOrDefault(s string) string {
	switch s {
	case "price_asc", "price_desc", "length_desc", "year_desc", "recent":
		return s
	default:
		return "recent"
	}
}

func sortListings(ls []listing.Listing, mode string) {
	switch mode {
	case "price_asc":
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].PriceCents < ls[j].PriceCents })
	case "price_desc":
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].PriceCents > ls[j].PriceCents })
	case "length_desc":
		sort.SliceStable(ls, func(i, j int) bool {
			return views.AttrFloat(ls[i], "longueur_m") > views.AttrFloat(ls[j], "longueur_m")
		})
	case "year_desc":
		sort.SliceStable(ls, func(i, j int) bool { return views.AttrFloat(ls[i], "annee") > views.AttrFloat(ls[j], "annee") })
	default: // recent : Search renvoie déjà created_at DESC
	}
}

// render enveloppe le contenu dans le shell templux (même coquille que le core).
func render(w http.ResponseWriter, r *http.Request, title string, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ctx := middleware.WithPageURI(r.Context(), r.RequestURI)
	if err := webui.Shell(title, content).Render(ctx, w); err != nil {
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
	}
}
