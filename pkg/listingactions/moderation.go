// Actions de modération communautaire du marketplace : signalement (geste de
// membre) et masquage/restauration (geste de modérateur). Même contrat que
// provider.go — l'identité du reporter vient TOUJOURS du contexte
// (middleware.UserFromContext), jamais d'un paramètre ; un membre ne peut pas se
// déclarer modérateur via un param, le RequiredPerm élevé s'en charge à la
// bordure (la perm de masquage n'est accordée qu'au grade modérateur).
package listingactions

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/listing"
)

// PermReport couvre le signalement d'une annonce : geste de membre, accordé au
// grade système membre par la bordure (comme les favoris).
const PermReport = "listing.report"

// PermModerate couvre le masquage/restauration d'une annonce : geste de
// modérateur, accordé à un grade ÉLEVÉ (sys-moderator) par la bordure — jamais
// au grade membre. C'est ce RequiredPerm qui rejette un non-modérateur.
const PermModerate = "listing.moderate"

// ModeratorPerms retourne les permissions à accorder à un grade ÉLEVÉ
// (modérateur/admin), distinctes des perms membre de Perms(). La bordure les
// octroie à sys-moderator, jamais à sys-member.
func ModeratorPerms() []string { return []string{PermModerate} }

func reportAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.report",
		Title: "Signaler une annonce",
		Description: "Signale une annonce abusive (contenu trompeur, illégal, hors-charte). Le " +
			"signaleur est l'utilisateur courant déduit du contexte d'authentification — jamais " +
			"un paramètre. Un motif libre est requis. Le signalement est enregistré pour revue " +
			"par un modérateur ; il ne masque pas l'annonce de lui-même.",
		RequiredPerm: PermReport,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["listing_id","reason"],
			"properties":{
				"listing_id":{"type":"string","minLength":1},
				"reason":{"type":"string","minLength":1,"maxLength":1000}
			}
		}`),
		Run: func(ctx context.Context, _ app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return errUnauth()
			}
			var p struct {
				ListingID string `json:"listing_id"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return badParams(err)
			}
			// reporterID = identité du contexte, jamais un paramètre.
			r, err := store.ReportListing(ctx, u.ID, p.ListingID, p.Reason)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce signalée.",
				Data:    map[string]any{"id": r.ID, "listing_id": r.ListingID, "reporter_id": r.ReporterID},
			}, nil
		},
	}
}

func moderateHideAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.moderate_hide",
		Title: "Masquer une annonce (modération)",
		Description: "Masque une annonce : elle disparaît des recherches quel que soit son statut " +
			"vendeur, sans modifier ce statut (un published masqué reste published pour son " +
			"propriétaire). Geste de modération réservé au grade modérateur ; un membre ordinaire " +
			"est refusé par la permission requise. Idempotent.",
		RequiredPerm: PermModerate,
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
			if err := store.Hide(ctx, p.ListingID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce masquée.",
				Data:    map[string]any{"listing_id": p.ListingID, "hidden": true},
			}, nil
		},
	}
}

func moderateRestoreAction(store *listing.Store) actions.Action {
	return actions.Action{
		ID:    "listing.moderate_restore",
		Title: "Restaurer une annonce (modération)",
		Description: "Retire le masquage de modération d'une annonce : elle redevient visible en " +
			"recherche selon son statut vendeur. Geste de modération réservé au grade modérateur ; " +
			"un membre ordinaire est refusé par la permission requise. Idempotent.",
		RequiredPerm: PermModerate,
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
			if err := store.Unhide(ctx, p.ListingID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			return actions.Result{
				Status:  "ok",
				Message: "Annonce restaurée.",
				Data:    map[string]any{"listing_id": p.ListingID, "hidden": false},
			}, nil
		},
	}
}
