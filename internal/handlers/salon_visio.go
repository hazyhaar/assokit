// CLAUDE:SUMMARY Route GET /salon/{slug}/visio — page enclave VanJS pour la salle de visioconférence. L4.
// Garde : appartenance + visio_enabled (L2a conservée).
// Routes de modération hôte : POST /salon/{slug}/visio/{mute_participant|remove_participant|end} (L2b réutilisées).
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/livekit"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// MountSalonVisioRoute câble GET /salon/{slug}/visio si le Vault LiveKit est disponible.
// Sans Vault, la route reste montée mais répond 503 (pas de panique, pas de bypass).
// Les routes de modération hôte (mute_participant, remove_participant, end) sont montées
// uniquement si le Vault est présent — elles appellent directement le Connector (L2b).
func MountSalonVisioRoute(r chi.Router, deps app.AppDeps, vault *assets.Vault) {
	var conn *livekit.Connector
	if vault != nil {
		conn = livekit.New(vault)
	}
	r.Get("/salon/{slug}/visio", handleSalonVisio(deps, conn))
	if conn != nil {
		r.Post("/salon/{slug}/visio/mute_participant", handleVisioMuteParticipant(deps, conn))
		r.Post("/salon/{slug}/visio/remove_participant", handleVisioRemoveParticipant(deps, conn))
		r.Post("/salon/{slug}/visio/end", handleVisioEnd(deps, conn))
	}
	// Consentement RGPD : hôte-only, indépendant du connecteur LiveKit.
	r.Post("/salon/{slug}/visio/transcription", handleVisioTranscriptionConsent(deps))
}

// visioConfig est la structure sérialisée en JSON dans la page enclave.
// Jamais concaténée en brut dans le JS — toujours via json.Marshal + htmlEscape.
type visioConfig struct {
	Token                string `json:"token"`
	URL                  string `json:"url"`
	Room                 string `json:"room"`
	Slug                 string `json:"slug"`
	SalonName            string `json:"salonName"`
	IsOwner              bool   `json:"isOwner"`
	CSRFToken            string `json:"csrfToken"`
	TranscriptionConsent bool   `json:"transcriptionConsent"`
}

// handleSalonVisio sert la page enclave VanJS de visioconférence pour un salon.
// Gardes : authentification + appartenance + visio_enabled + connecteur LiveKit configuré.
func handleSalonVisio(deps app.AppDeps, conn *livekit.Connector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		slug := chi.URLParam(r, "slug")
		store := &salon.Store{DB: deps.DB}

		sl, err := store.Get(r.Context(), slug)
		if err != nil {
			http.Error(w, "Salon introuvable", http.StatusNotFound)
			return
		}

		if !sl.VisioEnabled {
			http.Error(w, "Visioconférence non activée pour ce salon", http.StatusForbidden)
			return
		}

		ok, err := store.IsMember(r.Context(), slug, u.ID)
		if err != nil {
			deps.Logger.Warn("salon_visio_member_check_failed", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "Accès réservé aux membres du salon", http.StatusForbidden)
			return
		}

		// Garde connecteur : après les gardes d'appartenance pour renvoyer 403 en priorité.
		if conn == nil {
			http.Error(w, "Visioconférence non configurée", http.StatusServiceUnavailable)
			return
		}

		// Forge du jeton participant (CanPublish + CanSubscribe, pas de roomAdmin).
		token, serverURL, err := conn.RoomAccess(r.Context(), sl.Slug, u.ID, time.Hour)
		if err != nil {
			deps.Logger.Warn("salon_visio_room_access_failed", "slug", slug, "err", err.Error())
			http.Error(w, "Visioconférence non configurée", http.StatusServiceUnavailable)
			return
		}

		isOwner, err := store.IsOwner(r.Context(), slug, u.ID)
		if err != nil {
			// Non-fatal : l'erreur d'IsOwner ne prive pas l'accès, juste les contrôles hôte.
			isOwner = false
		}

		cfg := visioConfig{
			Token:                token,
			URL:                  serverURL,
			Room:                 sl.Slug,
			Slug:                 sl.Slug,
			SalonName:            sl.Name,
			IsOwner:              isOwner,
			CSRFToken:            middleware.CSRFToken(r.Context()),
			TranscriptionConsent: sl.TranscriptionConsent,
		}
		cfgJSON, err := json.Marshal(cfg)
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		// Bannière RGPD : visible par tous les membres quand le consentement est actif.
		// L'hôte voit en plus le bouton de bascule du consentement.
		banner := ""
		if sl.TranscriptionConsent {
			banner = `<div id="transcript-banner" style="position:fixed;top:0;left:0;right:0;z-index:9999;background:#b45309;color:#fff;text-align:center;padding:0.5rem 1rem;font-size:0.9rem;">
Cette réunion est transcrite.</div>`
		}
		ownerToggle := ""
		if isOwner {
			csrfVal := htmlEscape(middleware.CSRFToken(r.Context()))
			if sl.TranscriptionConsent {
				ownerToggle = `<form method="post" action="/salon/` + htmlEscape(sl.Slug) + `/visio/transcription" style="position:fixed;top:2.5rem;right:1rem;z-index:9999;">
<input type="hidden" name="gorilla.csrf.Token" value="` + csrfVal + `">
<input type="hidden" name="consent" value="0">
<button type="submit" style="background:#1f2937;color:#fff;border:1px solid #6b7280;padding:0.3rem 0.8rem;border-radius:4px;cursor:pointer;font-size:0.8rem;">Désactiver la transcription</button>
</form>`
			} else {
				ownerToggle = `<form method="post" action="/salon/` + htmlEscape(sl.Slug) + `/visio/transcription" style="position:fixed;top:0.5rem;right:1rem;z-index:9999;">
<input type="hidden" name="gorilla.csrf.Token" value="` + csrfVal + `">
<input type="hidden" name="consent" value="1">
<button type="submit" style="background:#1f2937;color:#fff;border:1px solid #6b7280;padding:0.3rem 0.8rem;border-radius:4px;cursor:pointer;font-size:0.8rem;">Activer la transcription</button>
</form>`
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="fr">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>` + htmlEscape(sl.Name) + ` — Visioconférence</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0;}
body{background:#18181b;color:#fff;font-family:system-ui,sans-serif;height:100vh;overflow:hidden;}
#visio-root{height:100vh;}
button{cursor:pointer;}
</style>
</head>
<body>
` + banner + ownerToggle + `
<!-- Config injectée côté serveur : JSON échappé, jamais concaténé en JS brut. -->
<script id="visio-config" type="application/json">` + string(cfgJSON) + `</script>
<div id="visio-root"></div>
<script src="/static/js/van.js"></script>
<script src="/static/js/livekit-client.umd.min.js"></script>
<script src="/static/js/visio.js"></script>
</body>
</html>`))
	}
}

// visioModParams est la structure des paramètres POST pour les actions de modération hôte.
type visioModParams struct {
	TargetIdentity string `json:"target_identity"`
}

// requireSalonOwner est un helper de garde inline pour les routes de modération visio.
// Retourne false et écrit la réponse HTTP si la garde échoue.
func requireSalonOwner(w http.ResponseWriter, r *http.Request, deps app.AppDeps, slug string) bool {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		http.Error(w, "Authentification requise", http.StatusUnauthorized)
		return false
	}
	store := &salon.Store{DB: deps.DB}
	ok, err := store.IsOwner(r.Context(), slug, u.ID)
	if err != nil {
		deps.Logger.Warn("visio_mod_owner_check", "slug", slug, "err", err.Error())
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
		return false
	}
	if !ok {
		http.Error(w, "Réservé au owner du salon", http.StatusForbidden)
		return false
	}
	return true
}

// handleVisioMuteParticipant coupe la piste audio d'un participant (modération hôte).
// Réutilise conn.MuteParticipant de L2b — aucun recodage.
func handleVisioMuteParticipant(deps app.AppDeps, conn *livekit.Connector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		if !requireSalonOwner(w, r, deps, slug) {
			return
		}
		var p visioModParams
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.TargetIdentity == "" {
			http.Error(w, "Paramètre target_identity requis", http.StatusBadRequest)
			return
		}
		if err := conn.MuteParticipant(r.Context(), slug, p.TargetIdentity); err != nil {
			deps.Logger.Warn("visio_mute_participant_failed", "slug", slug, "target", p.TargetIdentity, "err", err.Error())
			http.Error(w, "Erreur LiveKit", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// handleVisioRemoveParticipant expulse un participant de la salle (modération hôte).
// Réutilise conn.RemoveParticipant de L2b — aucun recodage.
func handleVisioRemoveParticipant(deps app.AppDeps, conn *livekit.Connector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		if !requireSalonOwner(w, r, deps, slug) {
			return
		}
		var p visioModParams
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.TargetIdentity == "" {
			http.Error(w, "Paramètre target_identity requis", http.StatusBadRequest)
			return
		}
		if err := conn.RemoveParticipant(r.Context(), slug, p.TargetIdentity); err != nil {
			deps.Logger.Warn("visio_remove_participant_failed", "slug", slug, "target", p.TargetIdentity, "err", err.Error())
			http.Error(w, "Erreur LiveKit", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// handleVisioTranscriptionConsent bascule le consentement RGPD à la transcription (hôte-only).
// POST /salon/{slug}/visio/transcription — paramètre form "consent" : "1" active, "0" désactive.
// Gardé par requireSalonOwner (même garde que mute/remove/end).
func handleVisioTranscriptionConsent(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		if !requireSalonOwner(w, r, deps, slug) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Paramètre invalide", http.StatusBadRequest)
			return
		}
		on := r.FormValue("consent") == "1"
		st := &salon.Store{DB: deps.DB}
		if err := st.ToggleTranscriptionConsent(r.Context(), slug, on); err != nil {
			deps.Logger.Warn("visio_transcription_consent_failed", "slug", slug, "on", on, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		// Redirection vers la page visio pour rafraîchir la bannière.
		http.Redirect(w, r, "/salon/"+slug+"/visio", http.StatusSeeOther)
	}
}

// handleVisioEnd termine la salle LiveKit pour tous les participants (modération hôte).
// Réutilise conn.EndRoom de L2b — aucun recodage.
func handleVisioEnd(deps app.AppDeps, conn *livekit.Connector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		if !requireSalonOwner(w, r, deps, slug) {
			return
		}
		if err := conn.EndRoom(r.Context(), slug); err != nil {
			deps.Logger.Warn("visio_end_room_failed", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur LiveKit", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
