// CLAUDE:SUMMARY Store de la file de publication réseaux sociaux sur la DB cœur de
// l'instance : enqueue, posts dus, journal d'idempotence par (post, réseau).
// CLAUDE:WARN Single-writer = le worker. Aucun ATTACH cross-DB, aucune référence à
// jobs.db horos55 : la planification est LOCALE au DB cœur.
package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hazyhaar/assokit/pkg/uid"
)

// ErrNotFound est retourné quand un post n'existe pas dans la file.
var ErrNotFound = errors.New("social: post non trouvé")

// ErrInvalidOutcome est retourné par ResolvePublishing pour un outcome hors du
// couple autorisé ('sent' confirmée, 'failed' re-tentable).
var ErrInvalidOutcome = errors.New("social: outcome de résolution invalide (attendu 'sent' ou 'failed')")

// DefaultOrphanAge est le seuil d'âge par défaut au-delà duquel une ligne de
// journal restée 'publishing' est considérée orpheline (diffusion partie en vol,
// issue jamais confirmée). Une marge confortable : un Publish + MarkPublished
// normal résout la ligne en quelques secondes.
const DefaultOrphanAge = 15 * time.Minute

// DraftPendingURL est la valeur sentinelle de PostRef.URL signalant un BROUILLON :
// une remise déposée chez le provider mais NON publiée (typiquement un brouillon TikTok
// à valider à la main dans l'app du provider), par opposition à une URL publique réelle.
// C'est la source de vérité, au niveau du journal, de la distinction brouillon/publié.
// Le connecteur tiktok porte aujourd'hui sa propre constante DraftPending de même
// valeur : un import direct créerait un cycle (tiktok importe déjà social), donc la
// valeur est volontairement miroir. Toute évolution doit garder les deux alignées ;
// à terme, tiktok devrait référencer cette constante-ci.
const DraftPendingURL = "brouillon soumis — en attente de validation manuelle dans la boîte TikTok"

// PublishStateDraft et PublishStatePublished sont les valeurs de la colonne
// social_post_log.publish_state : 'draft' = brouillon à valider manuellement,
// 'published' = publication publique réelle.
const (
	PublishStateDraft     = "draft"
	PublishStatePublished = "published"
)

// publishStateFor déduit l'état journalisé d'une remise à partir de sa référence : un
// brouillon (URL sentinelle DraftPendingURL) n'est PAS une publication publique.
func publishStateFor(ref PostRef) string {
	if ref.URL == DraftPendingURL {
		return PublishStateDraft
	}
	return PublishStatePublished
}

// PublishOutcome est l'issue tranchée manuellement pour une ligne orpheline.
type PublishOutcome string

const (
	// OutcomeConfirmed verrouille la ligne en 'sent' : la diffusion est confirmée
	// chez le provider (vérification hors-bande), aucune republication ne suivra.
	OutcomeConfirmed PublishOutcome = "sent"
	// OutcomeRetry inscrit la ligne en 'failed' : la diffusion n'est pas partie,
	// elle redevient re-tentable au prochain tick du worker.
	OutcomeRetry PublishOutcome = "failed"
)

// OrphanPublication décrit une ligne de journal restée 'publishing' au-delà du
// seuil d'âge : une diffusion partie en vol dont l'issue n'a jamais été confirmée
// ('sent') ni inscrite en échec franc ('failed'), typiquement après un crash entre
// le Publish réussi côté provider et MarkPublished.
type OrphanPublication struct {
	PostID    string        `json:"post_id"`
	Network   string        `json:"network"`
	CreatedAt time.Time     `json:"created_at"`
	Age       time.Duration `json:"age"`
}

// Store encapsule l'accès aux tables social_post_queue et social_post_log.
type Store struct {
	DB *sql.DB
}

// NewStore construit un Store. Les tables sont supposées créées par la migration
// chassis (00038) appliquée au boot de la bordure.
func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

// Enqueue insère un post en file (state=pending). Si IdempotencyKey est fournie et
// déjà présente, l'insertion est ignorée (INSERT OR IGNORE) et l'id existant est
// retourné — un double enqueue ne crée pas de doublon. L'id est un UUIDv7 (pkg/uid).
func (s *Store) Enqueue(ctx context.Context, p SocialPost) (string, error) {
	if len(p.Networks) == 0 {
		return "", fmt.Errorf("social.Enqueue: aucun réseau cible")
	}
	if p.ID == "" {
		p.ID = uid.New()
	}
	if p.ScheduledAt.IsZero() {
		p.ScheduledAt = time.Now().UTC()
	}
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = p.ID
	}
	media, err := json.Marshal(p.MediaPaths)
	if err != nil {
		return "", fmt.Errorf("social.Enqueue marshal media: %w", err)
	}
	nets, err := json.Marshal(p.Networks)
	if err != nil {
		return "", fmt.Errorf("social.Enqueue marshal networks: %w", err)
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO social_post_queue
		  (id, content, media_paths, networks, scheduled_at, idempotency_key, state, attempts)
		VALUES (?,?,?,?,?,?,?,0)`,
		p.ID, p.Content, string(media), string(nets),
		p.ScheduledAt.UTC().Format(time.RFC3339), p.IdempotencyKey, string(StatePending),
	)
	if err != nil {
		return "", fmt.Errorf("social.Enqueue: %w", err)
	}
	// Récupérer l'id réellement en file (le nôtre si inséré, l'existant si la clé
	// d'idempotence a fait ignorer l'insertion).
	var id string
	err = s.DB.QueryRowContext(ctx,
		`SELECT id FROM social_post_queue WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("social.Enqueue resolve id: %w", err)
	}
	return id, nil
}

// DuePosts retourne les posts pending dont l'échéance est atteinte (scheduled_at
// <= now), du plus ancien au plus récent, limités à limit.
func (s *Store) DuePosts(ctx context.Context, now time.Time, limit int) ([]SocialPost, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, content, media_paths, networks, scheduled_at, idempotency_key, state, attempts, last_error
		FROM social_post_queue
		WHERE state=? AND scheduled_at <= ?
		ORDER BY scheduled_at ASC
		LIMIT ?`,
		string(StatePending), now.UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("social.DuePosts: %w", err)
	}
	defer rows.Close()

	var out []SocialPost
	for rows.Next() {
		var (
			p           SocialPost
			media, nets string
			sched       string
			state       string
		)
		if err := rows.Scan(&p.ID, &p.Content, &media, &nets, &sched, &p.IdempotencyKey, &state, &p.Attempts, &p.LastError); err != nil {
			return nil, fmt.Errorf("social.DuePosts scan: %w", err)
		}
		if err := json.Unmarshal([]byte(media), &p.MediaPaths); err != nil {
			return nil, fmt.Errorf("social.DuePosts media %s: %w", p.ID, err)
		}
		if err := json.Unmarshal([]byte(nets), &p.Networks); err != nil {
			return nil, fmt.Errorf("social.DuePosts networks %s: %w", p.ID, err)
		}
		p.ScheduledAt, _ = time.Parse(time.RFC3339, sched)
		p.State = State(state)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PublishStatus retourne le statut de la ligne de journal (post, réseau) : "sent"
// (diffusion confirmée), "publishing" (diffusion en vol / orpheline si elle subsiste),
// "failed" (échec franc re-tentable), ou "" si aucune ligne n'existe encore.
//
// C'est la lecture du verrou d'idempotence AT-MOST-ONCE et la base de la DISTINCTION
// que le worker doit faire : 'sent' = réseau réellement diffusé (terminal) ; 'publishing'
// = diffusion en vol dont l'issue est INCONNUE (typiquement un crash entre le Publish
// réussi côté provider et MarkPublished). Republier un 'publishing' risquerait un doublon
// visible chez le provider, donc le worker ne republie ni 'sent' ni 'publishing' ; mais,
// à la différence de 'sent', un 'publishing' n'autorise PAS la promotion du post en
// StateSent — il le parque en attente de réconciliation (cf. worker.publishPost). Un
// échec réseau franc, lui, est inscrit 'failed' par MarkPublishFailed et reste re-tentable.
func (s *Store) PublishStatus(ctx context.Context, postID, network string) (string, error) {
	var status string
	err := s.DB.QueryRowContext(ctx,
		`SELECT status FROM social_post_log WHERE post_id=? AND network=?`,
		postID, network).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("social.PublishStatus: %w", err)
	}
	return status, nil
}

// OrphanPublishing liste les lignes de journal restées 'publishing' plus anciennes
// que olderThan, du plus ancien au plus récent. Chaque entrée est une diffusion
// partie en vol jamais résolue (ni 'sent', ni 'failed') : elle demande une
// vérification manuelle chez le provider puis une résolution via ResolvePublishing.
func (s *Store) OrphanPublishing(ctx context.Context, olderThan time.Duration) ([]OrphanPublication, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT post_id, network, created_at
		FROM social_post_log
		WHERE status='publishing' AND created_at <= ?
		ORDER BY created_at ASC`,
		cutoff.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("social.OrphanPublishing: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	var out []OrphanPublication
	for rows.Next() {
		var (
			o       OrphanPublication
			created string
		)
		if err := rows.Scan(&o.PostID, &o.Network, &created); err != nil {
			return nil, fmt.Errorf("social.OrphanPublishing scan: %w", err)
		}
		o.CreatedAt, _ = time.Parse(time.RFC3339, created)
		o.Age = now.Sub(o.CreatedAt)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolvePublishing tranche manuellement le sort d'une ligne orpheline restée
// 'publishing' après vérification hors-bande chez le provider : OutcomeConfirmed la
// verrouille 'sent' (diffusion confirmée, jamais republiée), OutcomeRetry l'inscrit
// 'failed' (re-tentable au prochain tick). Idempotent : l'UPDATE ne porte QUE sur les
// lignes encore 'publishing' — relancer la résolution d'une ligne déjà tranchée est un
// no-op (aucune ligne affectée, aucune erreur).
func (s *Store) ResolvePublishing(ctx context.Context, postID, network string, outcome PublishOutcome) error {
	if outcome != OutcomeConfirmed && outcome != OutcomeRetry {
		return fmt.Errorf("social.ResolvePublishing: %w", ErrInvalidOutcome)
	}
	note := ""
	if outcome == OutcomeRetry {
		note = "réconciliation manuelle: diffusion non confirmée, re-tentable"
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE social_post_log
		SET status=?, error=?, created_at=?
		WHERE post_id=? AND network=? AND status='publishing'`,
		string(outcome), note, time.Now().UTC().Format(time.RFC3339), postID, network,
	)
	if err != nil {
		return fmt.Errorf("social.ResolvePublishing: %w", err)
	}
	return nil
}

// ClaimPublish pose la marque d'idempotence AVANT l'appel Publish : une ligne de
// journal (post, réseau) en statut 'publishing'. INSERT OR IGNORE → si la ligne
// existe déjà (re-tentative après échec, ou succès antérieur), elle est conservée.
func (s *Store) ClaimPublish(ctx context.Context, postID, network string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO social_post_log (id, post_id, network, status, created_at)
		VALUES (?,?,?,'publishing',?)`,
		uid.New(), postID, network, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("social.ClaimPublish: %w", err)
	}
	return nil
}

// MarkPublished promeut la ligne de journal (post, réseau) en statut 'sent' et y
// inscrit la référence de remise (post_ref, url, coût). publish_state distingue une
// publication publique réelle ('published') d'un brouillon déposé non publié ('draft',
// déduit de la référence via publishStateFor). Idempotent.
func (s *Store) MarkPublished(ctx context.Context, postID string, ref PostRef) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE social_post_log
		SET status='sent', post_ref=?, post_url=?, cost_cents=?, publish_state=?, error='', created_at=?
		WHERE post_id=? AND network=?`,
		ref.ExternalID, ref.URL, ref.CostCents, publishStateFor(ref),
		time.Now().UTC().Format(time.RFC3339), postID, ref.Network,
	)
	if err != nil {
		return fmt.Errorf("social.MarkPublished: %w", err)
	}
	return nil
}

// MarkPublishFailed inscrit l'échec d'une publication (post, réseau) dans le
// journal sans bloquer la re-tentative : le statut reste 'failed', re-tentable au
// prochain tick tant que le post n'est pas définitivement abandonné par le worker.
func (s *Store) MarkPublishFailed(ctx context.Context, postID, network, reason string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE social_post_log
		SET status='failed', error=?, created_at=?
		WHERE post_id=? AND network=?`,
		reason, time.Now().UTC().Format(time.RFC3339), postID, network,
	)
	if err != nil {
		return fmt.Errorf("social.MarkPublishFailed: %w", err)
	}
	return nil
}

// MarkPostState met à jour l'état agrégé d'un post en file et le compteur de
// tentatives, en consignant la dernière erreur le cas échéant.
func (s *Store) MarkPostState(ctx context.Context, postID string, state State, attempts int, lastErr string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE social_post_queue
		SET state=?, attempts=?, last_error=?, updated_at=?
		WHERE id=?`,
		string(state), attempts, lastErr, time.Now().UTC().Format(time.RFC3339), postID,
	)
	if err != nil {
		return fmt.Errorf("social.MarkPostState: %w", err)
	}
	return nil
}
