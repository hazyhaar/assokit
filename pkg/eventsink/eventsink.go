// Package eventsink définit l'interface de sortie d'événements du kit (côté
// consommateur, doctrine interface-côté-consommateur). Le core émet des
// événements métier (nouveau membre, feedback, post forum, paiement…) vers un
// Sink abstrait ; la bordure injecte l'implémentation concrète.
//
// Deux implémentations import-clean (publiables) sont fournies : NopSink (défaut,
// inerte) et HTTPSink (webhook HTTP générique signé HMAC). Un adaptateur vers le
// bus ledger horos55 (mode pôle) implémente la même interface et s'injecte à la
// bordure SANS coupler le core à horos55 — préservant la frontière d'export.
package eventsink

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event est un événement métier émis par le core.
type Event struct {
	Type    string         `json:"type"`    // ex: "feedback.created", "member.signup"
	Payload map[string]any `json:"payload"` // données sérialisables JSON
}

// Sink reçoit les événements émis par le core. Implémentations à la bordure.
type Sink interface {
	Emit(ctx context.Context, e Event) error
}

// NopSink ignore tous les événements (défaut quand aucune sortie n'est configurée).
type NopSink struct{}

// Emit ne fait rien.
func (NopSink) Emit(context.Context, Event) error { return nil }

// HTTPSink poste chaque événement en JSON sur une URL de webhook générique.
// Si Secret est non vide, ajoute un en-tête X-Assokit-Signature = HMAC-SHA256
// hex du corps, permettant au récepteur de vérifier l'authenticité.
// Import-clean (publiable) : aucune dépendance horos55.
type HTTPSink struct {
	URL    string
	Secret string
	Client *http.Client
}

// Emit envoie l'événement. Best-effort : une erreur réseau est retournée mais
// l'appelant côté handler ne doit pas bloquer la réponse HTTP dessus.
func (s HTTPSink) Emit(ctx context.Context, e Event) error {
	body, err := json.Marshal(struct {
		Event
		Timestamp string `json:"timestamp"`
	}{Event: e, Timestamp: nowRFC3339(ctx)})
	if err != nil {
		return fmt.Errorf("eventsink: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("eventsink: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Secret != "" {
		mac := hmac.New(sha256.New, []byte(s.Secret))
		mac.Write(body)
		req.Header.Set("X-Assokit-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("eventsink: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("eventsink: webhook a répondu %d", resp.StatusCode)
	}
	return nil
}

// ctxTimeKey permet d'injecter un horodatage déterministe en test.
type ctxTimeKey struct{}

// WithTimestamp force l'horodatage des événements (tests). En production, laisser
// vide → time.Now() au moment de l'émission.
func WithTimestamp(ctx context.Context, ts string) context.Context {
	return context.WithValue(ctx, ctxTimeKey{}, ts)
}

func nowRFC3339(ctx context.Context) string {
	if v, ok := ctx.Value(ctxTimeKey{}).(string); ok && v != "" {
		return v
	}
	return time.Now().UTC().Format(time.RFC3339)
}
