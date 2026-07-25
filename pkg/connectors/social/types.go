// CLAUDE:SUMMARY Socle de publication réseaux sociaux : types de file (SocialPost),
// référence de publication (PostRef) et interface Network définie CÔTÉ CONSOMMATEUR.
// CLAUDE:WARN Aucun connecteur réseau réel ici (Meta = G1). Import-clean (publiable) :
// stdlib + pkg/uid uniquement, zéro dépendance horos55, zéro tenant_id.
package social

import (
	"context"
	"time"
)

// State est l'état d'un post dans la file social_post_queue.
type State string

const (
	// StatePending : post en attente de publication (ou re-tentable au prochain tick).
	StatePending State = "pending"
	// StateSent : tous les réseaux cibles ont été publiés avec succès.
	StateSent State = "sent"
	// StateFailed : retries épuisés sur au moins un réseau cible.
	StateFailed State = "failed"
)

// SocialPost est une publication à diffuser sur un ou plusieurs réseaux.
// L'id est un UUIDv7 (pkg/uid). IdempotencyKey est la clé fournie à l'enqueue :
// deux enqueues portant la même clé ne créent qu'une entrée de file (dédoublonnage
// d'insertion). La non-republication par réseau, elle, est garantie par le journal
// social_post_log (cf. Store).
type SocialPost struct {
	ID             string    `db:"id"              json:"id"`
	Content        string    `db:"content"         json:"content"`
	MediaPaths     []string  `db:"media_paths"     json:"media_paths"`
	Networks       []string  `db:"networks"        json:"networks"`
	ScheduledAt    time.Time `db:"scheduled_at"    json:"scheduled_at"`
	IdempotencyKey string    `db:"idempotency_key" json:"idempotency_key"`
	State          State     `db:"state"           json:"state"`
	Attempts       int       `db:"attempts"        json:"attempts"`
	LastError      string    `db:"last_error"      json:"last_error,omitempty"`
}

// PostRef est la référence retournée par un réseau après publication réussie.
type PostRef struct {
	Network    string `json:"network"`     // id du réseau (ex "facebook")
	ExternalID string `json:"external_id"` // id natif du post chez le provider
	URL        string `json:"url"`         // URL publique du post, si fournie
	CostCents  int    `json:"cost_cents"`  // coût éventuel de la publication, en centimes
}

// Network est l'interface qu'un connecteur réseau doit satisfaire pour être
// publié par le worker. Définie CÔTÉ CONSOMMATEUR (le worker), jamais imposée par
// un connecteur concret : le cœur social ne dépend d'aucun réseau particulier.
// En G0 aucune implémentation réelle n'existe ; le worker se câble avec les
// implémentations qui lui sont enregistrées à la bordure (Meta arrive en G1).
type Network interface {
	// ID retourne l'identifiant stable du réseau (ex "facebook", "instagram").
	ID() string
	// Publish diffuse le post et retourne sa référence native. Une erreur est
	// re-tentée au tick suivant (bornée par maxAttempts côté worker).
	Publish(ctx context.Context, p SocialPost) (PostRef, error)
}
