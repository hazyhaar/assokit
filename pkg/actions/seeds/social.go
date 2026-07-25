package seeds

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
)

// initSocial enregistre les actions de publication réseaux sociaux. L'action
// social.meta.publish met un post en FILE (social_post_queue) ciblant une ou
// plusieurs surfaces Meta ; le worker social (avec le connecteur Meta câblé à la
// bordure) en assure la publication réelle et l'idempotence at-most-once. L'action
// ne touche aucun provider : elle enquête uniquement la file via le Store existant.
func initSocial(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.meta.publish",
		Title:        "Publier sur Meta (Instagram / Facebook)",
		Description:  "Met une publication en file pour diffusion sur les surfaces Meta (Instagram et/ou Facebook). La diffusion réelle est assurée par le worker social ; un même idempotency_key ne crée qu'une entrée.",
		RequiredPerm: "social.meta.publish",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["content","networks"],
			"properties":{
				"content":{"type":"string","minLength":1,"description":"Texte / légende de la publication"},
				"media_paths":{"type":"array","items":{"type":"string"},"description":"Chemins ou URLs des médias (image / vidéo verticale 9:16)"},
				"networks":{"type":"array","minItems":1,"items":{"type":"string","enum":["instagram","facebook"]},"description":"Surfaces Meta cibles"},
				"scheduled_at":{"type":"string","format":"date-time","description":"Échéance de publication (RFC3339). Vide = immédiat"},
				"idempotency_key":{"type":"string","description":"Clé de dédoublonnage de l'enqueue (vide = id généré)"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Content        string   `json:"content"`
				MediaPaths     []string `json:"media_paths"`
				Networks       []string `json:"networks"`
				ScheduledAt    string   `json:"scheduled_at"`
				IdempotencyKey string   `json:"idempotency_key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			var scheduled time.Time
			if p.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, p.ScheduledAt)
				if err != nil {
					return actions.Result{Status: "error", Message: fmt.Sprintf("scheduled_at invalide: %v", err)}, nil
				}
				scheduled = t
			}
			store := social.NewStore(deps.DB)
			id, err := store.Enqueue(ctx, social.SocialPost{
				Content:        p.Content,
				MediaPaths:     p.MediaPaths,
				Networks:       p.Networks,
				ScheduledAt:    scheduled,
				IdempotencyKey: p.IdempotencyKey,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: "Publication mise en file.",
				Data:    map[string]any{"post_id": id, "networks": p.Networks},
			}, nil
		},
	})
	initSocialX(reg)
	initSocialYouTube(reg)
	initSocialTikTok(reg)
	initSocialLinkedIn(reg)
	initSocialReconcile(reg)
}

// initSocialReconcile enregistre l'action de RÉCONCILIATION d'une diffusion orpheline.
// Une ligne de journal (post, réseau) restée en statut 'publishing' signale une
// diffusion partie en vol dont l'issue n'a jamais été confirmée — typiquement un crash
// entre le Publish réussi côté provider et la journalisation du succès. Le worker
// social parque alors le post (pending, sans re-tentative) au lieu de le déclarer
// faussement « envoyé » ou de l'épuiser en échec. Cette action tranche manuellement,
// après vérification chez le provider : outcome="sent" verrouille la diffusion comme
// confirmée (jamais republiée), outcome="failed" la rend re-tentable au prochain tick.
// La résolution est idempotente : relancer sur une ligne déjà tranchée est sans effet.
func initSocialReconcile(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.reconcile",
		Title:        "Réconcilier une diffusion sociale orpheline",
		Description:  "Tranche le sort d'une diffusion réseau restée en cours ('publishing') sans confirmation — une publication partie en vol dont l'issue est inconnue, qui parque le post en attente. Après vérification chez le provider : outcome 'sent' confirme la diffusion (verrou définitif, jamais republiée), outcome 'failed' la rend re-tentable au prochain passage du worker. Idempotent : sans effet sur une ligne déjà résolue. Lister les diffusions orphelines en attente est exposé par l'observation de la file ; cette action en résout une, identifiée par post_id + network.",
		RequiredPerm: "social.reconcile",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["post_id","network","outcome"],
			"properties":{
				"post_id":{"type":"string","minLength":1,"description":"Identifiant du post en file dont une diffusion est orpheline"},
				"network":{"type":"string","minLength":1,"description":"Réseau de la diffusion orpheline (ex instagram, facebook, x, youtube, tiktok, linkedin)"},
				"outcome":{"type":"string","enum":["sent","failed"],"description":"'sent' = diffusion confirmée chez le provider (verrou définitif) ; 'failed' = non partie, re-tentable au prochain tick"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				PostID  string `json:"post_id"`
				Network string `json:"network"`
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := social.NewStore(deps.DB)
			if err := store.ResolvePublishing(ctx, p.PostID, p.Network, social.PublishOutcome(p.Outcome)); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			msg := "Diffusion réconciliée : confirmée comme envoyée."
			if p.Outcome == string(social.OutcomeRetry) {
				msg = "Diffusion réconciliée : marquée non partie, re-tentable au prochain tick."
			}
			return actions.Result{
				Status:  "ok",
				Message: msg,
				Data:    map[string]any{"post_id": p.PostID, "network": p.Network, "outcome": p.Outcome},
			}, nil
		},
	})
}

// initSocialLinkedIn enregistre l'action de publication sur LinkedIn. Comme les autres
// actions sociales, elle met un post en FILE (social_post_queue) ciblant le réseau
// "linkedin" ; le worker social (avec le connecteur LinkedIn câblé à la bordure) en assure
// la publication réelle (Posts API), l'enregistrement d'asset image et l'idempotence
// at-most-once. LinkedIn ne facture pas d'API directe (coût nul). L'action ne touche aucun
// provider. NOTE : publier au nom d'une PAGE D'ORGANISATION exige l'habilitation LinkedIn
// Partner Program, démarche administrative hors de cette action.
func initSocialLinkedIn(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.linkedin.publish",
		Title:        "Publier sur LinkedIn",
		Description:  "Met une publication en file pour diffusion sur LinkedIn (Posts API). Un média image local est téléversé en asset puis référencé. Publier au nom d'une page d'organisation exige l'habilitation LinkedIn Partner Program (démarche administrative). La diffusion réelle est assurée par le worker social ; un même idempotency_key ne crée qu'une entrée.",
		RequiredPerm: "social.linkedin.publish",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["content"],
			"properties":{
				"content":{"type":"string","minLength":1,"description":"Texte de la publication"},
				"media_paths":{"type":"array","items":{"type":"string"},"description":"Chemins locaux des images à téléverser (asset image LinkedIn)"},
				"scheduled_at":{"type":"string","format":"date-time","description":"Échéance de publication (RFC3339). Vide = immédiat"},
				"idempotency_key":{"type":"string","description":"Clé de dédoublonnage de l'enqueue (vide = id généré)"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Content        string   `json:"content"`
				MediaPaths     []string `json:"media_paths"`
				ScheduledAt    string   `json:"scheduled_at"`
				IdempotencyKey string   `json:"idempotency_key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			var scheduled time.Time
			if p.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, p.ScheduledAt)
				if err != nil {
					return actions.Result{Status: "error", Message: fmt.Sprintf("scheduled_at invalide: %v", err)}, nil
				}
				scheduled = t
			}
			store := social.NewStore(deps.DB)
			id, err := store.Enqueue(ctx, social.SocialPost{
				Content:        p.Content,
				MediaPaths:     p.MediaPaths,
				Networks:       []string{"linkedin"},
				ScheduledAt:    scheduled,
				IdempotencyKey: p.IdempotencyKey,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: "Publication mise en file.",
				Data:    map[string]any{"post_id": id, "networks": []string{"linkedin"}},
			}, nil
		},
	})
}

// initSocialTikTok enregistre l'action de publication sur TikTok. Comme les autres
// actions sociales, elle met un post en FILE (social_post_queue) ciblant le réseau
// "tiktok" ; le worker social (avec le connecteur TikTok câblé à la bordure) en assure
// le dépôt réel. TikTok est vidéo-only : au moins un média vidéo est requis. MODÈLE
// BROUILLON-EN-BOÎTE : le connecteur dépose un BROUILLON dans la boîte de réception du
// compte, qui exige une validation MANUELLE avant publication réelle — l'action ne
// publie donc jamais directement. L'idempotence at-most-once du worker garantit qu'un
// même idempotency_key ne crée qu'une entrée.
func initSocialTikTok(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.tiktok.publish",
		Title:        "Publier sur TikTok (brouillon)",
		Description:  "Met une vidéo en file pour dépôt en BROUILLON dans la boîte de réception TikTok. TikTok est vidéo-only : au moins un média vidéo est requis. Le brouillon doit être validé MANUELLEMENT depuis l'application TikTok avant sa publication publique ; cette action ne publie pas directement. La diffusion (dépôt du brouillon) est assurée par le worker social ; un même idempotency_key ne crée qu'une entrée.",
		RequiredPerm: "social.tiktok.publish",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["content","media_paths"],
			"properties":{
				"content":{"type":"string","minLength":1,"description":"Légende de la vidéo"},
				"media_paths":{"type":"array","minItems":1,"items":{"type":"string"},"description":"Chemins locaux des vidéos à déposer (TikTok est vidéo-only, MP4 H.264 vertical 9:16)"},
				"scheduled_at":{"type":"string","format":"date-time","description":"Échéance de dépôt (RFC3339). Vide = immédiat"},
				"idempotency_key":{"type":"string","description":"Clé de dédoublonnage de l'enqueue (vide = id généré)"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Content        string   `json:"content"`
				MediaPaths     []string `json:"media_paths"`
				ScheduledAt    string   `json:"scheduled_at"`
				IdempotencyKey string   `json:"idempotency_key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			var scheduled time.Time
			if p.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, p.ScheduledAt)
				if err != nil {
					return actions.Result{Status: "error", Message: fmt.Sprintf("scheduled_at invalide: %v", err)}, nil
				}
				scheduled = t
			}
			store := social.NewStore(deps.DB)
			id, err := store.Enqueue(ctx, social.SocialPost{
				Content:        p.Content,
				MediaPaths:     p.MediaPaths,
				Networks:       []string{"tiktok"},
				ScheduledAt:    scheduled,
				IdempotencyKey: p.IdempotencyKey,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: "Vidéo mise en file pour dépôt en brouillon TikTok.",
				Data:    map[string]any{"post_id": id, "networks": []string{"tiktok"}},
			}, nil
		},
	})
}

// initSocialYouTube enregistre l'action de publication sur YouTube. Comme les autres
// actions sociales, elle met un post en FILE (social_post_queue) ciblant le réseau
// "youtube" ; le worker social (avec le connecteur YouTube câblé à la bordure) en assure
// le téléversement réel (videos.insert resumable) et l'idempotence at-most-once. YouTube
// est vidéo-only : au moins un média vidéo est requis (la diffusion échoue sinon). Pour
// un Short, inclure "#Shorts" dans le contenu. L'action ne touche aucun provider.
func initSocialYouTube(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.youtube.publish",
		Title:        "Publier sur YouTube",
		Description:  "Met une vidéo en file pour téléversement sur YouTube (videos.insert resumable). YouTube est vidéo-only : au moins un média vidéo est requis. Pour un Short, inclure #Shorts dans le contenu. La diffusion réelle est assurée par le worker social ; un même idempotency_key ne crée qu'une entrée.",
		RequiredPerm: "social.youtube.publish",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["content","media_paths"],
			"properties":{
				"content":{"type":"string","minLength":1,"description":"Texte : la première ligne sert de titre, l'ensemble de description. Inclure #Shorts pour un Short"},
				"media_paths":{"type":"array","minItems":1,"items":{"type":"string"},"description":"Chemins locaux des vidéos à téléverser (YouTube est vidéo-only)"},
				"scheduled_at":{"type":"string","format":"date-time","description":"Échéance de publication (RFC3339). Vide = immédiat"},
				"idempotency_key":{"type":"string","description":"Clé de dédoublonnage de l'enqueue (vide = id généré)"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Content        string   `json:"content"`
				MediaPaths     []string `json:"media_paths"`
				ScheduledAt    string   `json:"scheduled_at"`
				IdempotencyKey string   `json:"idempotency_key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			var scheduled time.Time
			if p.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, p.ScheduledAt)
				if err != nil {
					return actions.Result{Status: "error", Message: fmt.Sprintf("scheduled_at invalide: %v", err)}, nil
				}
				scheduled = t
			}
			store := social.NewStore(deps.DB)
			id, err := store.Enqueue(ctx, social.SocialPost{
				Content:        p.Content,
				MediaPaths:     p.MediaPaths,
				Networks:       []string{"youtube"},
				ScheduledAt:    scheduled,
				IdempotencyKey: p.IdempotencyKey,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: "Vidéo mise en file.",
				Data:    map[string]any{"post_id": id, "networks": []string{"youtube"}},
			}, nil
		},
	})
}

// initSocialX enregistre l'action de publication sur X (Twitter). Comme social.meta.publish,
// elle met un post en FILE (social_post_queue) ciblant le réseau "x" ; le worker social
// (avec le connecteur X câblé à la bordure) en assure la publication réelle, l'upload
// média v1 chunké et l'idempotence at-most-once. L'action ne touche aucun provider.
func initSocialX(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "social.x.publish",
		Title:        "Publier sur X (Twitter)",
		Description:  "Met une publication en file pour diffusion sur X (Twitter). La diffusion réelle est assurée par le worker social ; un même idempotency_key ne crée qu'une entrée. Le coût X est journalisé selon la présence d'un lien.",
		RequiredPerm: "social.x.publish",
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["content"],
			"properties":{
				"content":{"type":"string","minLength":1,"description":"Texte de la publication"},
				"media_paths":{"type":"array","items":{"type":"string"},"description":"Chemins locaux des médias à uploader (upload v1 chunké)"},
				"scheduled_at":{"type":"string","format":"date-time","description":"Échéance de publication (RFC3339). Vide = immédiat"},
				"idempotency_key":{"type":"string","description":"Clé de dédoublonnage de l'enqueue (vide = id généré)"}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Content        string   `json:"content"`
				MediaPaths     []string `json:"media_paths"`
				ScheduledAt    string   `json:"scheduled_at"`
				IdempotencyKey string   `json:"idempotency_key"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			var scheduled time.Time
			if p.ScheduledAt != "" {
				t, err := time.Parse(time.RFC3339, p.ScheduledAt)
				if err != nil {
					return actions.Result{Status: "error", Message: fmt.Sprintf("scheduled_at invalide: %v", err)}, nil
				}
				scheduled = t
			}
			store := social.NewStore(deps.DB)
			id, err := store.Enqueue(ctx, social.SocialPost{
				Content:        p.Content,
				MediaPaths:     p.MediaPaths,
				Networks:       []string{"x"},
				ScheduledAt:    scheduled,
				IdempotencyKey: p.IdempotencyKey,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: "Publication mise en file.",
				Data:    map[string]any{"post_id": id, "networks": []string{"x"}},
			}, nil
		},
	})
}
