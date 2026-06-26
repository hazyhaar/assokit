// CLAUDE:SUMMARY Handler /feedback : GET form partial + POST insert anonyme, honeypot, rate-limit, ip_hash SHA256.
package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/hazyhaar/assokit/pkg/uid"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/components"
	"github.com/hazyhaar/assokit/pkg/eventsink"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// handleFeedbackForm sert la partial modale (GET /feedback/form).
func handleFeedbackForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pageURL := r.URL.Query().Get("url")
		pageTitle := r.URL.Query().Get("title")
		csrfToken := middleware.CSRFToken(r.Context())

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := components.FeedbackForm(pageURL, pageTitle, csrfToken).Render(r.Context(), w); err != nil {
			deps.Logger.Error("feedback form render", "err", err)
		}
	}
}

// handleFeedbackPost traite la soumission (POST /feedback).
// Restreint aux usagers identifiés (M-ASSOKIT-FEEDBACK-WIDGET-CSS-MISSING) :
// le widget est anonyme dans la table feedbacks (pas de user_id stocké) MAIS
// l'utilisateur doit être loggué pour avoir le droit d'envoyer (anti-spam).
func handleFeedbackPost(deps app.AppDeps, rl *middleware.RateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Feedback ANONYME autorisé : c'est un canal de debug présent sur chaque page,
		// y compris les pages d'authentification (login, récupération de mot de passe)
		// où l'utilisateur n'est précisément pas connecté et où il rencontre le plus de
		// friction. Le record feedback ne référence aucun utilisateur ; honeypot +
		// rate-limit par ip_hash protègent du spam. (Audité 2026-06-13 : la garde
		// u==nil masquait le feedback exactement là où il est le plus utile.)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)

		// Honeypot
		if r.FormValue("website") != "" {
			deps.Logger.Debug("feedback_honeypot_triggered", "req_id", reqID)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			components.FeedbackSuccess().Render(ctx, w) //nolint:errcheck
			return
		}

		// ip_hash = SHA256(remoteIP || cookieSecret)
		remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		if remoteIP == "" {
			remoteIP = r.RemoteAddr
		}
		h := sha256.New()
		h.Write([]byte(remoteIP))
		h.Write(deps.Config.CookieSecret)
		ipHash := hex.EncodeToString(h.Sum(nil))
		ipHashShort := ipHash[:16]

		// Rate-limit par ip_hash
		if !rl.Allow(ipHash) {
			deps.Logger.Info("feedback_rate_limited",
				"req_id", reqID,
				"ip_hash_prefix", ipHashShort,
			)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			components.FeedbackSuccess().Render(ctx, w) //nolint:errcheck
			return
		}

		message := strings.TrimSpace(r.FormValue("message"))
		if len([]rune(message)) < 5 || len(message) > 2000 {
			http.Error(w, "Le message doit contenir entre 5 et 2000 caractères.", http.StatusBadRequest)
			return
		}

		pageURL := r.FormValue("page_url")
		pageTitle := r.FormValue("page_title")

		ua := r.UserAgent()
		if len(ua) > 500 {
			ua = ua[:500]
		}

		locale := r.Header.Get("Accept-Language")
		if len(locale) > 50 {
			locale = locale[:50]
		}

		id := uid.New()
		_, err := deps.DB.ExecContext(ctx,
			`INSERT INTO feedbacks(id, page_url, page_title, message, ip_hash, user_agent, locale)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, pageURL, pageTitle, message, ipHash, ua, locale,
		)
		if err != nil {
			deps.Logger.Error("feedback_insert_failed",
				"req_id", reqID,
				"ip_hash_prefix", ipHashShort,
				"err", err.Error(),
			)
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		deps.Logger.Info("feedback_created",
			"req_id", reqID,
			"feedback_id", id,
			"page_url", pageURL,
			"ip_hash_prefix", ipHashShort,
		)

		// Debug-channel LLM-centric : émet l'événement vers le Sink (webhook
		// générique ou bus ledger horos55 selon l'injection bordure). Best-effort,
		// ne bloque jamais la réponse. URL + titre de page = contexte de debug ;
		// le screenshot viendra enrichir le payload ultérieurement.
		if deps.EventSink != nil {
			if err := deps.EventSink.Emit(ctx, eventsink.Event{
				Type: "feedback.created",
				Payload: map[string]any{
					"feedback_id": id,
					"page_url":    pageURL,
					"page_title":  pageTitle,
					"message":     message,
				},
			}); err != nil {
				deps.Logger.Warn("feedback_event_emit_failed", "req_id", reqID, "err", err.Error())
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.FeedbackSuccess().Render(r.Context(), w) //nolint:errcheck
	}
}
