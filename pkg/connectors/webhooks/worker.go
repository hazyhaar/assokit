// CLAUDE:SUMMARY Worker drainer — claim pending + dispatch via Connector.HandleWebhook + retry backoff (M-ASSOKIT-SPRINT2-S4).
// CLAUDE:WARN Drainer log uniquement event_id+provider+status (jamais payload — risque PII leak).
package webhooks

import (
	"context"
	"log/slog"
	"time"

	"github.com/hazyhaar/assokit/pkg/connectors"
)

// Worker drainer pour webhook_events.
type Worker struct {
	Store        *Store
	Registry     *connectors.Registry
	Logger       *slog.Logger
	Tick         time.Duration // défaut 1s
	BatchSize    int           // défaut 10
	MaxAttempts  int           // défaut 4
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

func (w *Worker) tick() time.Duration {
	if w.Tick > 0 {
		return w.Tick
	}
	return time.Second
}

func (w *Worker) batchSize() int {
	if w.BatchSize > 0 {
		return w.BatchSize
	}
	return 10
}

func (w *Worker) maxAttempts() int {
	if w.MaxAttempts > 0 {
		return w.MaxAttempts
	}
	return 4
}

// RunDrainer démarre une goroutine qui ticke périodiquement et processe les pending.
// Bloquant, retourne quand ctx est cancelé.
func (w *Worker) RunDrainer(ctx context.Context) {
	ticker := time.NewTicker(w.tick())
	defer ticker.Stop()

	w.DrainOnce(ctx) // initial tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.DrainOnce(ctx)
		}
	}
}

// DrainOnce claim batch + dispatch via Connector.HandleWebhook.
func (w *Worker) DrainOnce(ctx context.Context) {
	events, err := w.Store.ClaimPending(ctx, w.batchSize())
	if err != nil {
		w.logger().Error("webhook_drain_claim_failed", "err", err.Error())
		return
	}
	if len(events) == 0 {
		return
	}

	for _, ev := range events {
		c, ok := w.Registry.Get(ev.Provider)
		if !ok {
			w.logger().Warn("webhook_provider_not_registered", "event_id", ev.ID, "provider", ev.Provider)
			_ = w.Store.MarkFailed(ctx, ev.ID, "provider not registered", w.maxAttempts())
			continue
		}
		if err := c.HandleWebhook(ctx, ev.EventType, []byte(ev.Payload)); err != nil {
			w.logger().Warn("webhook_handle_failed",
				"event_id", ev.ID, "provider", ev.Provider, "event_type", ev.EventType,
				"attempts", ev.Attempts+1, "err", err.Error())
			_ = w.Store.MarkFailed(ctx, ev.ID, err.Error(), w.maxAttempts())
			continue
		}
		_ = w.Store.MarkProcessed(ctx, ev.ID)
		w.logger().Info("webhook_processed",
			"event_id", ev.ID, "provider", ev.Provider, "event_type", ev.EventType)
	}
}
