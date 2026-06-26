package seeds

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// InitSalonVisio enregistre les actions room.* si le Vault LiveKit est configuré.
// Séparée de initSalon car elle dépend d'un connecteur optionnel (vault peut être nil).
// Appelée depuis pkg/api/api.go après InitAll.
//
// Actions enregistrées :
//   - room.join         : rejoint la visio (jeton participant, CanPublish+CanSubscribe, PAS roomAdmin)
//   - room.mute_participant  : coupe la piste audio d'un participant (acte serveur, roomAdmin)
//   - room.remove_participant: expulse un participant (acte serveur, roomAdmin)
//   - room.end          : termine la salle (acte serveur, roomAdmin)
//
// Note : room.mute_self N'EST PAS enregistré ici. Couper son propre micro est un acte
// navigateur local (WebRTC setEnabled), sans intervention serveur / MCP.
func InitSalonVisio(reg *actions.Registry, vault *assets.Vault) {
	var conn *livekit.Connector
	if vault != nil {
		conn = livekit.New(vault)
	}
	// Enregistre les actions salon.* de base (avec le Connector pour le cycle de vie
	// best-effort, ou nil si le Vault LiveKit n'est pas configuré).
	initSalon(reg, conn)
	if vault == nil {
		// Pas de Vault LiveKit : les actions room.* ne sont pas enregistrées.
		return
	}

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "room.join",
		Title:        "Rejoindre la visio d'un salon",
		Description:  "Forge un jeton participant LiveKit (CanPublish + CanSubscribe, pas de roomAdmin) pour la salle visio du salon. Réservé aux membres du salon avec visio_enabled.",
		RequiredPerm: "salon.use",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{
				"slug":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}

			store := &salon.Store{DB: deps.DB}

			sl, err := store.Get(ctx, p.Slug)
			if err != nil {
				return actions.Result{Status: "error", Message: "salon introuvable"}, nil
			}
			if !sl.VisioEnabled {
				return actions.Result{Status: "error", Message: "visio non activée pour ce salon"}, nil
			}

			ok, err := store.IsMember(ctx, p.Slug, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			if !ok {
				return actions.Result{Status: "error", Message: "accès réservé aux membres du salon"}, nil
			}

			// Nom de salle LiveKit = slug du salon. Jeton participant standard,
			// CanPublish + CanSubscribe uniquement (RoomToken ne pose pas roomAdmin).
			token, serverURL, err := conn.RoomAccess(ctx, sl.Slug, u.ID, time.Hour)
			if err != nil {
				return actions.Result{Status: "error", Message: "connecteur LiveKit non disponible"}, err
			}

			return actions.Result{
				Status:  "ok",
				Message: "Jeton forgé.",
				Data: map[string]any{
					"token": token,
					"url":   serverURL,
					"room":  sl.Slug,
				},
			}, nil
		},
	})

	// room.mute_participant — coupe la piste audio publiée d'un participant.
	// Acte serveur via RoomService Twirp (roomAdmin token) : pas de mutation DB.
	// Réservé au owner du salon.
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "room.mute_participant",
		Title:        "Couper le micro d'un participant (modération)",
		Description:  "Coupe la piste audio publiée du participant `target_identity` dans la salle visio du salon. Acte serveur via RoomService LiveKit (roomAdmin). Réservé au owner du salon. No-op si le participant n'a pas de piste audio active.",
		RequiredPerm: "salon.use",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","target_identity"],
			"properties":{
				"slug":            {"type":"string","minLength":1},
				"target_identity": {"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug           string `json:"slug"`
				TargetIdentity string `json:"target_identity"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			// Vérifier que le salon existe et que l'appelant en est le owner.
			ok, err := store.IsOwner(ctx, p.Slug, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: "salon introuvable"}, nil
			}
			if !ok {
				return actions.Result{Status: "error", Message: "réservé au owner du salon"}, nil
			}
			if err := conn.MuteParticipant(ctx, p.Slug, p.TargetIdentity); err != nil {
				slog.ErrorContext(ctx, "room.mute_participant: MuteParticipant", "err", err, "room", p.Slug, "target", p.TargetIdentity)
				return actions.Result{Status: "error", Message: "impossible de couper le micro : " + err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Micro coupé (ou participant sans piste audio active)."}, nil
		},
	})

	// room.remove_participant — expulse un participant de la salle.
	// Acte serveur via RoomService Twirp (roomAdmin token) : pas de mutation DB.
	// Réservé au owner du salon.
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "room.remove_participant",
		Title:        "Expulser un participant (modération)",
		Description:  "Expulse `target_identity` de la salle visio du salon. Acte serveur via RoomService LiveKit (roomAdmin). Réservé au owner du salon.",
		RequiredPerm: "salon.use",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug","target_identity"],
			"properties":{
				"slug":            {"type":"string","minLength":1},
				"target_identity": {"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug           string `json:"slug"`
				TargetIdentity string `json:"target_identity"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			ok, err := store.IsOwner(ctx, p.Slug, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: "salon introuvable"}, nil
			}
			if !ok {
				return actions.Result{Status: "error", Message: "réservé au owner du salon"}, nil
			}
			if err := conn.RemoveParticipant(ctx, p.Slug, p.TargetIdentity); err != nil {
				slog.ErrorContext(ctx, "room.remove_participant: RemoveParticipant", "err", err, "room", p.Slug, "target", p.TargetIdentity)
				return actions.Result{Status: "error", Message: "impossible d'expulser le participant : " + err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Participant expulsé."}, nil
		},
	})

	// room.end — termine la salle visio (DeleteRoom LiveKit).
	// Acte serveur via RoomService Twirp (roomAdmin token) : pas de mutation DB.
	// Réservé au owner du salon.
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "room.end",
		Title:        "Terminer la salle visio",
		Description:  "Supprime la salle visio du salon (tous les participants sont déconnectés). Acte serveur via RoomService LiveKit (roomAdmin). Réservé au owner du salon.",
		RequiredPerm: "salon.use",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["slug"],
			"properties":{
				"slug":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			var p struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &salon.Store{DB: deps.DB}
			ok, err := store.IsOwner(ctx, p.Slug, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: "salon introuvable"}, nil
			}
			if !ok {
				return actions.Result{Status: "error", Message: "réservé au owner du salon"}, nil
			}
			if err := conn.EndRoom(ctx, p.Slug); err != nil {
				slog.ErrorContext(ctx, "room.end: EndRoom", "err", err, "room", p.Slug)
				return actions.Result{Status: "error", Message: "impossible de terminer la salle : " + err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Salle visio terminée."}, nil
		},
	})
}
