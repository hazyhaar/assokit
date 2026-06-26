// Package listingactions expose les opérations marketplace de pkg/listing en
// Actions enregistrées dans le registry assokit. Via RegisterActions, chaque
// action obtient route HTTP admin + outil MCP + permission RBAC automatiques
// (LLM-parité) — un agent pilote la marketplace exactement comme un membre humain.
//
// Frontière dure : ce provider CONSOMME pkg/listing (Store, méthodes) ; il ne le
// modifie pas. Le cœur pkg/listing reste import-clean (n'importe ni pkg/actions
// ni ce package). L'identité effective (owner pour create/update/set_status,
// buyer pour open_inquiry) vient TOUJOURS du contexte d'authentification
// (middleware.UserFromContext), JAMAIS des paramètres de l'action.
package listingactions

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/messaging"
)

// Permissions RBAC des actions marketplace. Les opérations d'écriture sont des
// gestes de membre (publier/éditer sa propre annonce, contacter un vendeur), pas
// des gestes d'administration — la bordure les accorde à un grade membre.
const (
	PermManage  = "listing.manage"  // create / update / set_status — owner-scopé
	PermInquire = "listing.inquire" // open_inquiry — buyer = user courant
	PermSearch  = "listing.search"  // recherche en lecture
)

// Perms retourne les permissions introduites par ce provider, pour que la
// bordure les accorde au grade approprié (cf. GrantToGrade des stores RBAC).
func Perms() []string {
	return []string{PermManage, PermInquire, PermSearch, PermFavorite, PermReport}
}

// Register injecte les 5 actions marketplace dans reg en capturant le Store
// silo-spécifique (lié à la DB du silo, distincte de la DB core). Le Messenger
// d'open_inquiry est construit au Run depuis la DB core (deps.DB), où vit la
// messagerie. Destiné au hook api.Options.RegisterActions.
//
// store doit être non nil : un provider sans store est une erreur de câblage.
func Register(store *listing.Store) func(*actions.Registry) error {
	return func(reg *actions.Registry) error {
		if store == nil {
			return errors.New("listingactions.Register: store nil")
		}
		if err := reg.Add(createAction(store)); err != nil {
			return err
		}
		if err := reg.Add(updateAction(store)); err != nil {
			return err
		}
		if err := reg.Add(setStatusAction(store)); err != nil {
			return err
		}
		if err := reg.Add(openInquiryAction(store)); err != nil {
			return err
		}
		if err := reg.Add(searchAction(store)); err != nil {
			return err
		}
		// Actions côté acheteur (favoris + recherches sauvées), cf. buyer.go.
		if err := reg.Add(favoriteAction(store)); err != nil {
			return err
		}
		if err := reg.Add(unfavoriteAction(store)); err != nil {
			return err
		}
		if err := reg.Add(listFavoritesAction(store)); err != nil {
			return err
		}
		if err := reg.Add(saveSearchAction(store)); err != nil {
			return err
		}
		if err := reg.Add(listSavedSearchesAction(store)); err != nil {
			return err
		}
		// Modération communautaire (signalement membre + masquage modérateur), cf.
		// moderation.go.
		if err := reg.Add(reportAction(store)); err != nil {
			return err
		}
		if err := reg.Add(moderateHideAction(store)); err != nil {
			return err
		}
		return reg.Add(moderateRestoreAction(store))
	}
}

// errUnauth est le résultat standard quand le contexte ne porte aucun utilisateur
// authentifié. Aucune action d'écriture ne dérive d'identité hors du contexte.
func errUnauth() (actions.Result, error) {
	return actions.Result{Status: "error", Message: "authentification requise"}, nil
}

func badParams(err error) (actions.Result, error) {
	return actions.Result{Status: "error", Message: err.Error()}, nil
}

func createAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.create",
		Title: "Publier une annonce",
		Description: "Crée une annonce marketplace dont le vendeur (owner) est l'utilisateur " +
			"courant, déduit du contexte d'authentification — jamais d'un paramètre. " +
			"Renseigner titre, description, prix en centimes et attributs métier du silo. " +
			"L'annonce naît en brouillon (draft) ; la publier ensuite via listing.set_status.",
		RequiredPerm: PermManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["title"],
			"properties":{
				"title":{"type":"string","minLength":1,"maxLength":300},
				"body":{"type":"string"},
				"price_cents":{"type":"integer","minimum":0},
				"attrs":{"type":"object"},
				"medias":{"type":"array","items":{"type":"string"}}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				Title      string         `json:"title"`
				Body       string         `json:"body"`
				PriceCents int64          `json:"price_cents"`
				Attrs      map[string]any `json:"attrs"`
				Medias     []string       `json:"medias"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			// OwnerID = identité du contexte, jamais un champ de params : un appelant
			// ne peut pas publier au nom d'autrui.
			l := &listing.Listing{
				OwnerID:    u.ID,
				Title:      p.Title,
				Body:       p.Body,
				PriceCents: p.PriceCents,
				Attrs:      p.Attrs,
				Medias:     p.Medias,
			}
			if err := store.Create(ctx, l); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce créée (brouillon).",
				Data:    map[string]any{"id": l.ID, "status": string(l.Status), "owner_id": l.OwnerID},
			}, nil
		},
	}
}

func updateAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.update",
		Title: "Modifier une annonce",
		Description: "Édite une annonce existante. Owner-scopé : l'utilisateur courant " +
			"(contexte d'authentification) doit être le vendeur de l'annonce, sinon " +
			"l'opération est refusée. Les champs absents restent inchangés.",
		RequiredPerm: PermManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"title":{"type":"string","minLength":1,"maxLength":300},
				"body":{"type":"string"},
				"price_cents":{"type":"integer","minimum":0},
				"attrs":{"type":"object"},
				"medias":{"type":"array","items":{"type":"string"}}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ID         string         `json:"id"`
				Title      *string        `json:"title"`
				Body       *string        `json:"body"`
				PriceCents *int64         `json:"price_cents"`
				Attrs      map[string]any `json:"attrs"`
				Medias     []string       `json:"medias"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			// callerID = identité du contexte ; l'owner-scoping de pkg/listing rejette
			// l'édition de l'annonce d'un tiers (ErrNotOwner).
			l, err := store.Update(ctx, u.ID, p.ID, listing.Patch{
				Title:      p.Title,
				Body:       p.Body,
				PriceCents: p.PriceCents,
				Attrs:      p.Attrs,
				Medias:     p.Medias,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce mise à jour.",
				Data:    map[string]any{"id": l.ID, "status": string(l.Status)},
			}, nil
		},
	}
}

func setStatusAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.set_status",
		Title: "Changer le statut d'une annonce",
		Description: "Applique une transition de cycle de vie (draft → published → sold/archived, " +
			"ou dépublication published → draft). Owner-scopé sur l'utilisateur courant ; " +
			"toute transition illégale est refusée par la validation du cœur.",
		RequiredPerm: PermManage,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id","status"],
			"properties":{
				"id":{"type":"string","minLength":1},
				"status":{"type":"string","enum":["draft","published","sold","archived"]}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			l, err := store.SetStatus(ctx, u.ID, p.ID, listing.Status(p.Status))
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Statut mis à jour.",
				Data:    map[string]any{"id": l.ID, "status": string(l.Status)},
			}, nil
		},
	}
}

func openInquiryAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.open_inquiry",
		Title: "Contacter un vendeur",
		Description: "Ouvre (ou récupère si déjà ouverte) une conversation avec le vendeur d'une " +
			"annonce. L'acheteur (buyer) est l'utilisateur courant déduit du contexte " +
			"d'authentification — jamais un paramètre. Idempotent par paire (annonce, acheteur).",
		RequiredPerm: PermInquire,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["listing_id"],
			"properties":{
				"listing_id":{"type":"string","minLength":1},
				"body":{"type":"string"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ListingID string `json:"listing_id"`
				Body      string `json:"body"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			// La messagerie vit dans la DB core (deps.DB), distincte de la DB silo des
			// annonces. buyerID = identité du contexte, jamais un paramètre.
			msgr := &messaging.Store{DB: deps.DB}
			inq, err := store.OpenInquiry(ctx, msgr, p.ListingID, u.ID, p.Body)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Conversation ouverte avec le vendeur.",
				Data: map[string]any{
					"listing_id": inq.ListingID,
					"buyer_id":   inq.BuyerID,
					"owner_id":   inq.OwnerID,
					"message_id": inq.MessageID,
				},
			}, nil
		},
	}
}

func searchAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.search",
		Title: "Rechercher des annonces",
		Description: "Recherche en lecture dans la marketplace : texte plein (FTS5), facettes " +
			"exactes par attribut métier, filtres de fourchette (prix ou attribut numérique), " +
			"filtre par statut et par vendeur. Retourne les annonces correspondantes.",
		RequiredPerm: PermSearch,
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"text":{"type":"string"},
				"facets":{"type":"object"},
				"status":{"type":"string","enum":["draft","published","sold","archived"]},
				"owner":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":100},
				"offset":{"type":"integer","minimum":0}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Text   string            `json:"text"`
				Facets map[string]string `json:"facets"`
				Status string            `json:"status"`
				Owner  string            `json:"owner"`
				Limit  int               `json:"limit"`
				Offset int               `json:"offset"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			results, err := store.Search(ctx, listing.Filter{
				Text:   p.Text,
				Facets: p.Facets,
				Status: listing.Status(p.Status),
				Owner:  p.Owner,
				Limit:  p.Limit,
				Offset: p.Offset,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Recherche effectuée.",
				Data:    map[string]any{"count": len(results), "listings": results},
			}, nil
		},
	}
}
