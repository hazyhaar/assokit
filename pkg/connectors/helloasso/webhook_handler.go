// CLAUDE:SUMMARY HelloAsso webhook handler — HMAC verify + dispatch event_type → donations UPSERT (M-ASSOKIT-SPRINT3-S2).
// CLAUDE:WARN HMAC compare via hmac.Equal (constant time, anti-timing-attack).
package helloasso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
)

// WebhookSignatureHeader : nom du header HelloAsso transportant la HMAC.
const WebhookSignatureHeader = "X-HelloAsso-Signature"

// VerifyWebhookSignature compare HMAC-SHA256(payload, secret) avec la signature reçue.
// Constant time via hmac.Equal pour bloquer timing attacks.
func VerifyWebhookSignature(payload []byte, signature, secret string) bool {
	if secret == "" {
		return false
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// VerifyWebhookFromVault charge le secret via vault et vérifie la signature.
func VerifyWebhookFromVault(ctx context.Context, vault *assets.Vault, payload []byte, signature string) (bool, error) {
	if vault == nil {
		return false, errors.New("vault nil")
	}
	ok := false
	err := vault.Use(ctx, "helloasso", "webhook_signing_secret", func(secret string) error {
		ok = VerifyWebhookSignature(payload, signature, secret)
		return nil
	})
	return ok, err
}

// envelope minimal HelloAsso webhook : eventType + data{...}.
type webhookEnvelope struct {
	EventType string          `json:"eventType"`
	Data      json.RawMessage `json:"data"`
}

// dataWithID : sous-payload data.id pour extraction event_id.
type dataWithID struct {
	ID json.Number `json:"id"`
}

// ExtractWebhookEventID parse le payload HelloAsso et retourne (event_id, event_type, err).
// Le receiver générique (handlers/webhook_receiver.go) appelle cet extractor pour
// remplir webhooks.Event{ID, EventType} avant Insert idempotent.
//
// Format HelloAsso : `{"eventType":"Payment.Notification","data":{"id":12345,...}}`.
// event_id = "<eventType>:<data.id>" pour garantir unicité cross-event-type
// (un même paiement peut générer plusieurs notifications de types différents).
func ExtractWebhookEventID(payload []byte) (eventID, eventType string, err error) {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", "", fmt.Errorf("ExtractWebhookEventID: parse envelope: %w", err)
	}
	if env.EventType == "" {
		return "", "", errors.New("ExtractWebhookEventID: eventType absent")
	}
	var data dataWithID
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return "", "", fmt.Errorf("ExtractWebhookEventID: parse data: %w", err)
	}
	if data.ID == "" {
		return "", "", errors.New("ExtractWebhookEventID: data.id absent")
	}
	return env.EventType + ":" + string(data.ID), env.EventType, nil
}

// ProcessWebhook dispatche un payload webhook HelloAsso.
// Le receiver générique (S2-4) a déjà fait HMAC verify + Insert webhook_events ;
// cette méthode est appelée par le worker drainer via Connector.HandleWebhook.
func (c *Connector) ProcessWebhook(ctx context.Context, db *sql.DB, eventType string, payload []byte, rawEventID string) error {
	if db == nil {
		return errors.New("ProcessWebhook: db requis")
	}
	store := &DonationsStore{DB: db}

	switch eventType {
	case "Order", "Order.Notification":
		return c.handleOrderEvent(ctx, store, payload, rawEventID)
	case "Payment", "Payment.Notification":
		return c.handlePaymentEvent(ctx, store, payload, rawEventID)
	case "Payment.Refunded":
		return c.handleRefundEvent(ctx, store, payload, rawEventID)
	default:
		return fmt.Errorf("helloasso: event_type %q non géré", eventType)
	}
}

// orderPayload représente un Order HelloAsso.
type orderPayload struct {
	ID       int64 `json:"id"`
	Payments []struct {
		ID         int64   `json:"id"`
		Amount     float64 `json:"amount"`
		State      string  `json:"state"`
		Date       string  `json:"date"`
		PayerEmail string  `json:"payerEmail"`
		PayerName  string  `json:"payerName"`
	} `json:"payments"`
	FormSlug string `json:"formSlug"`
	FormType string `json:"formType"`
}

func (c *Connector) handleOrderEvent(ctx context.Context, store *DonationsStore, payload []byte, rawEventID string) error {
	var data webhookEnvelope
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("handleOrder unmarshal envelope: %w", err)
	}
	body := data.Data
	if len(body) == 0 {
		body = payload
	}
	var order orderPayload
	if err := json.Unmarshal(body, &order); err != nil {
		return fmt.Errorf("handleOrder unmarshal data: %w", err)
	}
	for _, pay := range order.Payments {
		p := Payment{
			ID:         pay.ID,
			Amount:     pay.Amount,
			State:      pay.State,
			Date:       pay.Date,
			PayerEmail: pay.PayerEmail,
			PayerName:  pay.PayerName,
			FormSlug:   order.FormSlug,
		}
		if err := store.UpsertFromPayment(ctx, p, order.FormSlug, order.FormType, rawEventID); err != nil {
			return err
		}
	}
	return nil
}

// paymentPayload représente un Payment direct (event Payment.Notification).
type paymentPayload struct {
	ID         int64   `json:"id"`
	Amount     float64 `json:"amount"`
	State      string  `json:"state"`
	Date       string  `json:"date"`
	PayerEmail string  `json:"payerEmail"`
	PayerName  string  `json:"payerName"`
	FormSlug   string  `json:"formSlug"`
	FormType   string  `json:"formType"`
}

func (c *Connector) handlePaymentEvent(ctx context.Context, store *DonationsStore, payload []byte, rawEventID string) error {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("handlePayment unmarshal envelope: %w", err)
	}
	body := env.Data
	if len(body) == 0 {
		body = payload
	}
	var pay paymentPayload
	if err := json.Unmarshal(body, &pay); err != nil {
		return fmt.Errorf("handlePayment unmarshal data: %w", err)
	}
	p := Payment{
		ID: pay.ID, Amount: pay.Amount, State: pay.State, Date: pay.Date,
		PayerEmail: pay.PayerEmail, PayerName: pay.PayerName, FormSlug: pay.FormSlug,
	}
	return store.UpsertFromPayment(ctx, p, pay.FormSlug, pay.FormType, rawEventID)
}

func (c *Connector) handleRefundEvent(ctx context.Context, store *DonationsStore, payload []byte, rawEventID string) error {
	var env webhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("handleRefund unmarshal envelope: %w", err)
	}
	body := env.Data
	if len(body) == 0 {
		body = payload
	}
	var pay paymentPayload
	if err := json.Unmarshal(body, &pay); err != nil {
		return fmt.Errorf("handleRefund unmarshal data: %w", err)
	}
	// Force state="Refunded" (l'event type signale déjà le refund).
	pay.State = "Refunded"
	p := Payment{
		ID: pay.ID, Amount: pay.Amount, State: pay.State, Date: pay.Date,
		PayerEmail: pay.PayerEmail, PayerName: pay.PayerName, FormSlug: pay.FormSlug,
	}
	return store.UpsertFromPayment(ctx, p, pay.FormSlug, pay.FormType, rawEventID)
}
