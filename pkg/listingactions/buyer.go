// Actions côté acheteur du marketplace : favoris et recherches sauvées. Même
// contrat que provider.go — identité TOUJOURS issue du contexte
// (middleware.UserFromContext), jamais d'un paramètre. Un éventuel champ
// user_id injecté dans les params est ignoré (non déclaré au schéma, et l'identité
// effective est lue du contexte).
package listingactions

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/listing"
)

// PermFavorite couvre les gestes acheteur (favoris, recherches sauvées) : ce
// sont des gestes de membre, accordés au grade système membre par la bordure.
const PermFavorite = "listing.favorite"

func favoriteAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.favorite",
		Title: "Ajouter une annonce aux favoris",
		Description: "Marque une annonce comme favorite pour l'utilisateur courant, déduit du " +
			"contexte d'authentification — jamais d'un paramètre. Idempotent : ajouter deux " +
			"fois la même annonce ne crée pas de doublon.",
		RequiredPerm: PermFavorite,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["listing_id"],
			"properties":{
				"listing_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ListingID string `json:"listing_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			if err := store.AddFavorite(ctx, u.ID, p.ListingID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce ajoutée aux favoris.",
				Data:    map[string]any{"listing_id": p.ListingID, "user_id": u.ID},
			}, nil
		},
	}
}

func unfavoriteAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.unfavorite",
		Title: "Retirer une annonce des favoris",
		Description: "Retire une annonce des favoris de l'utilisateur courant (contexte " +
			"d'authentification). Idempotent : retirer une annonce non favorite n'est pas une erreur.",
		RequiredPerm: PermFavorite,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["listing_id"],
			"properties":{
				"listing_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ListingID string `json:"listing_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			if err := store.RemoveFavorite(ctx, u.ID, p.ListingID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce retirée des favoris.",
				Data:    map[string]any{"listing_id": p.ListingID, "user_id": u.ID},
			}, nil
		},
	}
}

func listFavoritesAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.list_favorites",
		Title: "Lister mes annonces favorites",
		Description: "Retourne les annonces favorites de l'utilisateur courant (contexte " +
			"d'authentification), plus récentes en premier. Un utilisateur ne voit que ses " +
			"propres favoris.",
		RequiredPerm: PermFavorite,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			results, err := store.ListFavorites(ctx, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Favoris listés.",
				Data:    map[string]any{"count": len(results), "listings": results},
			}, nil
		},
	}
}

func saveSearchAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.save_search",
		Title: "Sauvegarder une recherche",
		Description: "Mémorise une recherche nommée (mêmes critères que listing.search : texte, " +
			"facettes, fourchettes, statut) pour l'utilisateur courant déduit du contexte " +
			"d'authentification. Sert de base aux alertes : un nouveau listing correspondant " +
			"pourra être rapproché de cette recherche.",
		RequiredPerm: PermFavorite,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["name"],
			"properties":{
				"name":{"type":"string","minLength":1,"maxLength":200},
				"text":{"type":"string"},
				"facets":{"type":"object"},
				"status":{"type":"string","enum":["draft","published","sold","archived"]}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				Name   string            `json:"name"`
				Text   string            `json:"text"`
				Facets map[string]string `json:"facets"`
				Status string            `json:"status"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			ss, err := store.SaveSearch(ctx, u.ID, p.Name, listing.Filter{
				Text:   p.Text,
				Facets: p.Facets,
				Status: listing.Status(p.Status),
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Recherche sauvegardée.",
				Data:    map[string]any{"id": ss.ID, "name": ss.Name, "user_id": ss.UserID},
			}, nil
		},
	}
}

func listSavedSearchesAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.list_saved_searches",
		Title: "Lister mes recherches sauvegardées",
		Description: "Retourne les recherches sauvegardées de l'utilisateur courant (contexte " +
			"d'authentification), plus récentes en premier. Un utilisateur ne voit que ses propres " +
			"recherches.",
		RequiredPerm: PermFavorite,
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			results, err := store.ListSavedSearches(ctx, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			out := make([]map[string]any, 0, len(results))
			for _, ss := range results {
				out = append(out, map[string]any{
					"id":     ss.ID,
					"name":   ss.Name,
					"filter": ss.Filter,
				})
			}
			return actions.Result{
				Status:  "ok",
				Message: "Recherches sauvegardées listées.",
				Data:    map[string]any{"count": len(out), "saved_searches": out},
			}, nil
		},
	}
}
