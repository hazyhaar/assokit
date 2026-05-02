// CLAUDE:SUMMARY Receiver POST /webhooks/{provider} — HMAC verify + Insert idempotent (M-ASSOKIT-SPRINT2-S4).
// CLAUDE:WARN HMAC verify SE FAIT AVANT persist (anti-DoS DB). Payload jamais loggué (PII).
package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/webhooks"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// EventIDExtractor extrait event_id + event_type du payload provider-specific.
// Caller registers un extractor par provider via WebhookReceiver.RegisterExtractor.
type EventIDExtractor func(payload []byte) (eventID, eventType string, err error)

// SignatureHeaderName : nom du header HTTP qui porte la signature HMAC (provider-specific).
type SignatureConfig struct {
	HeaderName    string           // ex "X-Signature", "Stripe-Signature"
	ExtractEvent  EventIDExtractor // parse payload
}

// WebhookHandler handler chi.
func WebhookHandler(deps app.AppDeps, store *webhooks.Store, vault *assets.Vault, configs map[string]SignatureConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := chi.URLParam(r, "provider")
		reqID := middleware.RequestIDFromContext(r.Context())

		cfg, ok := configs[provider]
		if !ok {
			deps.Logger.Warn("webhook_provider_unknown", "req_id", reqID, "provider", provider)
			http.Error(w, "provider inconnu", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		if err != nil {
			http.Error(w, "body trop gros", http.StatusBadRequest)
			return
		}

		// HMAC verify AVANT persist (anti-DoS).
		signature := r.Header.Get(cfg.HeaderName)
		if signature == "" {
			deps.Logger.Warn("webhook_signature_missing", "req_id", reqID, "provider", provider)
			http.Error(w, "signature missing", http.StatusUnauthorized)
			return
		}
		if err := verifyHMAC(r.Context(), vault, provider, body, signature); err != nil {
			deps.Logger.Warn("webhook_hmac_invalid", "req_id", reqID, "provider", provider, "err", err.Error())
			http.Error(w, "signature invalide", http.StatusUnauthorized)
			return
		}

		eventID, eventType, err := cfg.ExtractEvent(body)
		if err != nil || eventID == "" {
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			deps.Logger.Warn("webhook_event_id_extract_failed", "req_id", reqID, "provider", provider, "err", errMsg)
			http.Error(w, "event_id absent", http.StatusBadRequest)
			return
		}

		ev := webhooks.Event{
			ID:        eventID,
			Provider:  provider,
			EventType: eventType,
			Payload:   string(body),
			Signature: signature,
		}
		err = store.Insert(r.Context(), ev)
		if errors.Is(err, webhooks.ErrDuplicate) {
			_ = store.MarkDuplicate(r.Context(), eventID)
			deps.Logger.Info("webhook_duplicate", "req_id", reqID, "provider", provider, "event_id", eventID)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"duplicate"}`)) //nolint:errcheck
			return
		}
		if err != nil {
			deps.Logger.Error("webhook_insert_failed", "req_id", reqID, "provider", provider, "err", err.Error())
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		deps.Logger.Info("webhook_received",
			"req_id", reqID, "provider", provider, "event_id", eventID, "event_type", eventType)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"received"}`)) //nolint:errcheck
	}
}

// verifyHMAC compare la signature reçue avec HMAC-SHA256(body, webhook_signing_secret).
// La secret est récupérée via Vault.Use (zero-out post-callback).
func verifyHMAC(ctx context.Context, vault *assets.Vault, provider string, body []byte, signature string) error {
	if vault == nil {
		return errors.New("vault non configuré")
	}
	expected := ""
	err := vault.Use(ctx, provider, "webhook_signing_secret", func(secret string) error {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected = hex.EncodeToString(mac.Sum(nil))
		return nil
	})
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return errors.New("HMAC mismatch")
	}
	return nil
}

var _ = fmt.Sprintf // keep import stable

// DefaultEventExtractor : parser JSON simple {"id":"...","type":"..."}.
// Fallback générique si le provider expose ces champs au top-level.
func DefaultEventExtractor(payload []byte) (string, string, error) {
	var p struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", "", err
	}
	if p.ID == "" {
		return "", "", errors.New("id manquant dans payload")
	}
	return p.ID, p.Type, nil
}
