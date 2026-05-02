// CLAUDE:SUMMARY Tests gardiens Worker drainer — process pending, retry backoff, max attempts (M-ASSOKIT-SPRINT2-S4).
package webhooks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hazyhaar/assokit/pkg/connectors"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// hookConnector capte les calls HandleWebhook + simule fail si configuré.
type hookConnector struct {
	id       string
	handles  atomic.Int32
	handleErr error
}

func (h *hookConnector) ID() string                                            { return h.id }
func (h *hookConnector) DisplayName() string                                   { return h.id }
func (h *hookConnector) Description() string                                   { return "" }
func (h *hookConnector) ConfigSchema() *jsonschema.Schema                      { return nil }
func (h *hookConnector) Start(ctx context.Context, cfg map[string]any) error  { return nil }
func (h *hookConnector) Stop(ctx context.Context) error                       { return nil }
func (h *hookConnector) Ping(ctx context.Context) (connectors.Health, error)  { return connectors.Health{OK: true}, nil }
func (h *hookConnector) HandleWebhook(ctx context.Context, eventType string, payload []byte) error {
	h.handles.Add(1)
	return h.handleErr
}

// TestWorker_DrainsPendingProcessesViaConnector : 3 pending → DrainOnce → 3 processed.
func TestWorker_DrainsPendingProcessesViaConnector(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	for _, id := range []string{"e1", "e2", "e3"} {
		_ = s.Insert(context.Background(), Event{ID: id, Provider: "helloasso", EventType: "x", Payload: "{}"})
	}
	reg := connectors.NewRegistry()
	c := &hookConnector{id: "helloasso"}
	_ = reg.Register(c)

	w := &Worker{Store: s, Registry: reg, MaxAttempts: 4}
	w.DrainOnce(context.Background())

	if c.handles.Load() != 3 {
		t.Errorf("HandleWebhook called %d times, attendu 3", c.handles.Load())
	}
	n, _ := s.CountByStatus(context.Background(), "helloasso", "processed")
	if n != 3 {
		t.Errorf("count processed = %d, attendu 3", n)
	}
}

// TestWorker_FailureMarksRetryAndIncrementAttempts : connector err → status='pending' + attempts=1 + next_retry_at.
func TestWorker_FailureMarksRetryAndIncrementAttempts(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	_ = s.Insert(context.Background(), Event{ID: "fail-evt", Provider: "p", EventType: "x", Payload: "{}"})
	reg := connectors.NewRegistry()
	_ = reg.Register(&hookConnector{id: "p", handleErr: errors.New("boom")})

	w := &Worker{Store: s, Registry: reg, MaxAttempts: 4}
	w.DrainOnce(context.Background())

	var status string
	var attempts int
	var nextRetry *string
	db.QueryRow(`SELECT status, attempts, next_retry_at FROM webhook_events WHERE id='fail-evt'`).Scan(&status, &attempts, &nextRetry)
	if status != "pending" || attempts != 1 {
		t.Errorf("post fail : status=%q attempts=%d, attendu pending+1", status, attempts)
	}
	if nextRetry == nil {
		t.Error("next_retry_at non posé après fail")
	}
}

// TestWorker_FinalFailureAfterMaxAttempts : MaxAttempts=2, 2 fails → status='failed'.
func TestWorker_FinalFailureAfterMaxAttempts(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	_ = s.Insert(context.Background(), Event{ID: "doomed", Provider: "p", EventType: "x", Payload: "{}"})
	reg := connectors.NewRegistry()
	_ = reg.Register(&hookConnector{id: "p", handleErr: errors.New("nope")})

	w := &Worker{Store: s, Registry: reg, MaxAttempts: 2}
	// Pour bypass next_retry_at delay : reset entre les drains.
	w.DrainOnce(context.Background()) // attempts=1, pending
	db.Exec(`UPDATE webhook_events SET next_retry_at=NULL WHERE id='doomed'`) //nolint:errcheck
	w.DrainOnce(context.Background()) // attempts=2, failed

	var status string
	var attempts int
	db.QueryRow(`SELECT status, attempts FROM webhook_events WHERE id='doomed'`).Scan(&status, &attempts)
	if status != "failed" {
		t.Errorf("après 2 fails : status=%q, attendu failed", status)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, attendu 2", attempts)
	}
}

// TestWorker_UnregisteredProviderMarksFailed : provider absent du Registry → MarkFailed.
func TestWorker_UnregisteredProviderMarksFailed(t *testing.T) {
	db := openWebhookDB(t)
	s := &Store{DB: db}
	_ = s.Insert(context.Background(), Event{ID: "ghost", Provider: "unknown", EventType: "x", Payload: "{}"})

	reg := connectors.NewRegistry()
	w := &Worker{Store: s, Registry: reg, MaxAttempts: 1}
	w.DrainOnce(context.Background())

	var status string
	db.QueryRow(`SELECT status FROM webhook_events WHERE id='ghost'`).Scan(&status)
	if status != "failed" {
		t.Errorf("provider unregistered : status=%q, attendu failed (MaxAttempts=1)", status)
	}
}
