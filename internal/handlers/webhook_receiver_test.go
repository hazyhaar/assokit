// CLAUDE:SUMMARY Tests gardiens WebhookHandler — HMAC verify, Insert idempotent, payload jamais loggué (M-ASSOKIT-SPRINT2-S4).
package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/webhooks"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

const webhookHandlerSchema = `
CREATE TABLE connectors (id TEXT PRIMARY KEY);
INSERT INTO connectors(id) VALUES ('helloasso');
CREATE TABLE connector_credentials (
	connector_id TEXT NOT NULL, key_name TEXT NOT NULL,
	encrypted_value BLOB NOT NULL,
	set_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	set_by TEXT, rotated_at TEXT,
	PRIMARY KEY(connector_id, key_name)
);
CREATE TABLE webhook_events (
	id TEXT PRIMARY KEY, provider TEXT NOT NULL, event_type TEXT NOT NULL,
	payload TEXT NOT NULL, signature TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending'
		CHECK (status IN ('pending','processing','processed','failed','duplicate')),
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	next_retry_at TEXT, processed_at TEXT
);
`

const webhookValidKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func setupWebhookHandlerTest(t *testing.T, secret string) (*sql.DB, *webhooks.Store, *assets.Vault) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(webhookHandlerSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	vault, _ := assets.NewVault(db, webhookValidKey)
	if err := vault.Set(context.Background(), "helloasso", "webhook_signing_secret", secret, "test"); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	store := &webhooks.Store{DB: db}
	return db, store, vault
}

func computeHMAC(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func newWebhookRouter(deps app.AppDeps, store *webhooks.Store, vault *assets.Vault) chi.Router {
	r := chi.NewRouter()
	configs := map[string]SignatureConfig{
		"helloasso": {HeaderName: "X-Signature", ExtractEvent: DefaultEventExtractor},
	}
	r.Post("/webhooks/{provider}", WebhookHandler(deps, store, vault, configs))
	return r
}

// TestWebhookReceiver_HMACInvalidReturns401 : signature wrong → 401, 0 INSERT.
func TestWebhookReceiver_HMACInvalidReturns401(t *testing.T) {
	db, store, vault := setupWebhookHandlerTest(t, "secret-1")
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := newWebhookRouter(deps, store, vault)

	body := `{"id":"evt-1","type":"payment.completed"}`
	req := httptest.NewRequest("POST", "/webhooks/helloasso", strings.NewReader(body))
	req.Header.Set("X-Signature", "ATTACKER_FAKE_SIG")
	req = req.WithContext(middleware.WithRequestID(req.Context(), "rq-1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("HMAC invalide code=%d, attendu 401", w.Code)
	}
	n, _ := store.CountByStatus(context.Background(), "helloasso", "pending")
	if n != 0 {
		t.Errorf("HMAC invalide a leaké INSERT : count pending = %d", n)
	}
}

// TestWebhookReceiver_HMACValidPersistsEvent : signature OK → 200 + 1 row pending.
func TestWebhookReceiver_HMACValidPersistsEvent(t *testing.T) {
	db, store, vault := setupWebhookHandlerTest(t, "secret-1")
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := newWebhookRouter(deps, store, vault)

	body := `{"id":"evt-ok","type":"payment.completed"}`
	sig := computeHMAC("secret-1", body)
	req := httptest.NewRequest("POST", "/webhooks/helloasso", strings.NewReader(body))
	req.Header.Set("X-Signature", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HMAC ok code=%d body=%s, attendu 200", w.Code, w.Body.String())
	}
	n, _ := store.CountByStatus(context.Background(), "helloasso", "pending")
	if n != 1 {
		t.Errorf("count pending = %d, attendu 1", n)
	}
}

// TestWebhookReceiver_DuplicateEventIDReturnsOkNoDoubleInsert : 2× même event_id → 200 + 1 row.
func TestWebhookReceiver_DuplicateEventIDReturnsOkNoDoubleInsert(t *testing.T) {
	db, store, vault := setupWebhookHandlerTest(t, "secret-1")
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := newWebhookRouter(deps, store, vault)

	body := `{"id":"evt-dup","type":"x"}`
	sig := computeHMAC("secret-1", body)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/webhooks/helloasso", strings.NewReader(body))
		req.Header.Set("X-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d code=%d", i, w.Code)
		}
	}

	var total int
	db.QueryRow(`SELECT COUNT(*) FROM webhook_events WHERE id='evt-dup'`).Scan(&total)
	if total != 1 {
		t.Errorf("dup INSERT count=%d, attendu 1", total)
	}
	var status string
	db.QueryRow(`SELECT status FROM webhook_events WHERE id='evt-dup'`).Scan(&status)
	if status != "duplicate" {
		t.Errorf("status après 2e POST = %q, attendu duplicate", status)
	}
}

// TestWebhookReceiver_PayloadNeverLoggedRaw : POST avec PII dans payload → slog ne contient pas le PII.
func TestWebhookReceiver_PayloadNeverLoggedRaw(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	db, store, vault := setupWebhookHandlerTest(t, "secret-1")
	deps := app.AppDeps{DB: db, Logger: logger}
	r := newWebhookRouter(deps, store, vault)

	body := `{"id":"evt-pii","type":"x","email":"victim@example.com","secret_phrase":"NEVER_LOG_THIS"}`
	sig := computeHMAC("secret-1", body)
	req := httptest.NewRequest("POST", "/webhooks/helloasso", strings.NewReader(body))
	req.Header.Set("X-Signature", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ok code=%d", w.Code)
	}

	if strings.Contains(buf.String(), "NEVER_LOG_THIS") || strings.Contains(buf.String(), "victim@example.com") {
		t.Errorf("payload PII leaké dans logs : %s", buf.String())
	}
}

// TestWebhookReceiver_UnknownProviderReturns404 : provider non configuré → 404.
func TestWebhookReceiver_UnknownProviderReturns404(t *testing.T) {
	db, store, vault := setupWebhookHandlerTest(t, "secret-1")
	deps := app.AppDeps{DB: db, Logger: slog.Default()}
	r := newWebhookRouter(deps, store, vault)

	req := httptest.NewRequest("POST", "/webhooks/inconnu", strings.NewReader(`{}`))
	req.Header.Set("X-Signature", "x")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("provider inconnu code=%d, attendu 404", w.Code)
	}
}
