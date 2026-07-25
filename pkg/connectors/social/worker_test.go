package social

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/chassis"

	_ "modernc.org/sqlite"
)

// recordingNetwork est un DOUBLE DE TEST (pas une donnée mock) : une impl de
// Network qui enregistre les posts publiés et retourne une PostRef synthétique.
// Permet de vérifier le câblage worker → Publish → journal sans connecteur réel.
type recordingNetwork struct {
	id        string
	mu        sync.Mutex
	published []string // ids de posts publiés, dans l'ordre
}

func (n *recordingNetwork) ID() string { return n.id }

func (n *recordingNetwork) Publish(_ context.Context, p SocialPost) (PostRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.published = append(n.published, p.ID)
	return PostRef{
		Network:    n.id,
		ExternalID: "ext-" + p.ID,
		URL:        "https://example.test/" + p.ID,
		CostCents:  0,
	}, nil
}

func (n *recordingNetwork) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.published)
}

// failingNetwork est un DOUBLE DE TEST : une impl de Network dont Publish échoue
// franchement à chaque appel (erreur réseau simulée), pour exercer la branche d'échec
// franc du worker sans connecteur réel.
type failingNetwork struct {
	id    string
	mu    sync.Mutex
	calls int
}

func (n *failingNetwork) ID() string { return n.id }

func (n *failingNetwork) Publish(_ context.Context, _ SocialPost) (PostRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return PostRef{}, errors.New("échec réseau simulé")
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := chassis.Run(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWorkerPublishesAndJournals(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	net := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": net}, time.Minute, silentLogger())

	id, err := store.Enqueue(ctx, SocialPost{
		Content:        "Sortie en mer dimanche",
		Networks:       []string{"facebook"},
		IdempotencyKey: "post-1",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.drainOnce(ctx)

	if net.count() != 1 {
		t.Fatalf("Publish appelé %d fois, attendu 1", net.count())
	}

	// Journal écrit avec la référence et le statut.
	var status, ref string
	err = db.QueryRow(`SELECT status, post_ref FROM social_post_log WHERE post_id=? AND network=?`,
		id, "facebook").Scan(&status, &ref)
	if err != nil {
		t.Fatalf("lecture journal: %v", err)
	}
	if status != "sent" || ref != "ext-"+id {
		t.Fatalf("journal = (%q,%q), attendu (sent, ext-%s)", status, ref, id)
	}

	// État agrégé du post promu à sent.
	var state string
	if err := db.QueryRow(`SELECT state FROM social_post_queue WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatalf("lecture file: %v", err)
	}
	if state != string(StateSent) {
		t.Fatalf("state = %q, attendu sent", state)
	}
}

func TestWorkerIdempotentSecondTick(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	net := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": net}, time.Minute, silentLogger())

	if _, err := store.Enqueue(ctx, SocialPost{
		Content:        "Annonce unique",
		Networks:       []string{"facebook"},
		IdempotencyKey: "post-unique",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.drainOnce(ctx) // 1er tick : publie
	w.drainOnce(ctx) // 2e tick : ne doit PAS re-publier

	if net.count() != 1 {
		t.Fatalf("Publish appelé %d fois après 2 ticks, attendu 1 (idempotence)", net.count())
	}
}

// TestAtMostOnce_CrashAfterPublishBeforeMark prouve la sémantique AT-MOST-ONCE : si
// un crash survient APRÈS un Publish réussi côté provider mais AVANT MarkPublished
// (la ligne de journal reste en 'publishing'), un tick ultérieur NE republie PAS.
// On simule le crash en posant la marque 'publishing' sans jamais la promouvoir.
func TestAtMostOnce_CrashAfterPublishBeforeMark(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	net := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": net}, time.Minute, silentLogger())

	id, err := store.Enqueue(ctx, SocialPost{
		Content:        "Publié chez le provider mais crash avant journalisation",
		Networks:       []string{"facebook"},
		IdempotencyKey: "post-crash",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Simule le déroulé worker JUSTE avant le crash : la marque d'idempotence est
	// posée AVANT l'appel Publish (qui a réussi côté provider), puis le process meurt
	// sans appeler MarkPublished — la ligne reste donc en 'publishing'.
	if err := store.ClaimPublish(ctx, id, "facebook"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Tick de reprise : le post est toujours pending en file, mais la paire
	// (post, facebook) est verrouillée 'publishing' → aucune republication.
	w.drainOnce(ctx)

	if net.count() != 0 {
		t.Fatalf("Publish appelé %d fois après crash, attendu 0 (at-most-once : pas de doublon)", net.count())
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM social_post_log WHERE post_id=? AND network=?`,
		id, "facebook").Scan(&status); err != nil {
		t.Fatalf("lecture journal: %v", err)
	}
	if status != "publishing" {
		t.Fatalf("status journal = %q, attendu publishing (verrou conservé)", status)
	}
}

// TestCrashInFlight_PostParkedNotSentNotFailed prouve le correctif d'idempotence : un
// post dont la SEULE diffusion non terminée est un orphelin 'publishing' (crash entre
// Publish réussi et MarkPublished) n'est JAMAIS faussement promu StateSent, ni poussé en
// StateFailed par l'épuisement des tentatives. Il reste parqué (pending, attempts=0) au
// fil des ticks, en attente d'une réconciliation manuelle.
func TestCrashInFlight_PostParkedNotSentNotFailed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	net := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": net}, time.Minute, silentLogger())

	id, err := store.Enqueue(ctx, SocialPost{
		Content:        "Diffusion partie en vol, crash avant journalisation du succès",
		Networks:       []string{"facebook"},
		IdempotencyKey: "post-inflight",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Reproduit le crash-en-vol : marque 'publishing' posée AVANT Publish (qui a réussi
	// côté provider), puis mort du process sans MarkPublished → ligne restée 'publishing'.
	if err := store.ClaimPublish(ctx, id, "facebook"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Plus de ticks que maxAttempts : si l'orphelin incrémentait attempts, le post
	// basculerait StateFailed. Il doit rester parqué pending sans abandon.
	for i := 0; i < maxAttempts+2; i++ {
		w.drainOnce(ctx)
	}

	if net.count() != 0 {
		t.Fatalf("Publish appelé %d fois, attendu 0 (orphelin jamais republié)", net.count())
	}

	var state string
	var attempts int
	if err := db.QueryRow(`SELECT state, attempts FROM social_post_queue WHERE id=?`, id).Scan(&state, &attempts); err != nil {
		t.Fatalf("lecture file: %v", err)
	}
	if state != string(StatePending) {
		t.Fatalf("state = %q, attendu pending (parqué, ni sent ni failed)", state)
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d, attendu 0 (orphelin n'incrémente pas vers l'échec)", attempts)
	}
}

// TestPartialOrphan_OneSentOneOrphan_NotPromotedSent prouve qu'un réseau réellement
// 'sent' ne suffit pas à promouvoir le post quand un autre réseau reste orphelin : le
// post est parqué (pending), pas faussement déclaré StateSent.
func TestPartialOrphan_OneSentOneOrphan_NotPromotedSent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	fb := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": fb}, time.Minute, silentLogger())

	id, err := store.Enqueue(ctx, SocialPost{
		Content:        "Deux réseaux, l'un diffusé, l'autre orphelin",
		Networks:       []string{"facebook", "instagram"},
		IdempotencyKey: "post-partial",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// instagram : orphelin (crash-en-vol simulé). facebook : sera diffusé par le worker.
	if err := store.ClaimPublish(ctx, id, "instagram"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	w.drainOnce(ctx)

	if got, _ := store.PublishStatus(ctx, id, "facebook"); got != "sent" {
		t.Fatalf("facebook status = %q, attendu sent", got)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM social_post_queue WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatalf("lecture file: %v", err)
	}
	if state != string(StatePending) {
		t.Fatalf("state = %q, attendu pending (un réseau orphelin bloque la promotion)", state)
	}
}

// TestReconcileOrphan_RetryThenPublished prouve le cycle complet de réconciliation : un
// orphelin parqué, listé par OrphanPublishing, résolu en 'failed' (re-tentable), est
// republié au tick suivant et le post atteint enfin StateSent.
func TestReconcileOrphan_RetryThenPublished(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	net := &recordingNetwork{id: "facebook"}
	w := NewWorker(store, map[string]Network{"facebook": net}, time.Minute, silentLogger())

	id, err := store.Enqueue(ctx, SocialPost{
		Content:        "Orphelin réconcilié puis republié",
		Networks:       []string{"facebook"},
		IdempotencyKey: "post-reconcile",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.ClaimPublish(ctx, id, "facebook"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	w.drainOnce(ctx) // parqué
	orphans, err := store.OrphanPublishing(ctx, 0)
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0].PostID != id || orphans[0].Network != "facebook" {
		t.Fatalf("orphans = %+v, attendu 1 (post %s, facebook)", orphans, id)
	}

	// Réconciliation : vérification hors-bande conclut « non partie » → re-tentable.
	if err := store.ResolvePublishing(ctx, id, "facebook", OutcomeRetry); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Idempotence : second appel = no-op.
	if err := store.ResolvePublishing(ctx, id, "facebook", OutcomeRetry); err != nil {
		t.Fatalf("resolve idempotent: %v", err)
	}

	w.drainOnce(ctx) // republie

	if net.count() != 1 {
		t.Fatalf("Publish appelé %d fois après réconciliation, attendu 1", net.count())
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM social_post_queue WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatalf("lecture file: %v", err)
	}
	if state != string(StateSent) {
		t.Fatalf("state = %q, attendu sent après réconciliation+republication", state)
	}
}

// TestOrphanCoexistsWithFrankFailure_ParkedNeverFailed prouve l'invariant C2 dans le
// cas adverse : un post ciblant À LA FOIS un réseau orphelin 'publishing' ET un réseau
// dont Publish échoue franchement ne doit JAMAIS partir en StateFailed (ni StateSent)
// tant que l'orphelin subsiste. Le parcage (hasOrphan) PRIME sur le retry d'échec franc :
// attempts ne s'épuise pas, le réseau orphelin n'est jamais republié, et le post reste
// drainable (pending) pour qu'une réconciliation ultérieure puisse le re-promouvoir.
func TestOrphanCoexistsWithFrankFailure_ParkedNeverFailed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		orphanNet  string
		failingNet string
	}{
		{"orphan_instagram_fail_facebook", "instagram", "facebook"},
		{"orphan_x_fail_youtube", "x", "youtube"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := newTestDB(t)
			store := NewStore(db)
			failing := &failingNetwork{id: tc.failingNet}
			// Le réseau orphelin reste verrouillé 'publishing' avant toute lecture du
			// registry : peu importe qu'il soit enregistré, il n'est jamais republié.
			w := NewWorker(store, map[string]Network{tc.failingNet: failing}, time.Minute, silentLogger())

			id, err := store.Enqueue(ctx, SocialPost{
				Content:        "Orphelin + échec franc coexistants",
				Networks:       []string{tc.orphanNet, tc.failingNet},
				IdempotencyKey: "post-" + tc.name,
			})
			if err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			// Crash-en-vol simulé sur le réseau orphelin.
			if err := store.ClaimPublish(ctx, id, tc.orphanNet); err != nil {
				t.Fatalf("claim: %v", err)
			}

			// Bien plus de ticks que maxAttempts : sans le primat de hasOrphan, l'échec
			// franc épuiserait attempts et basculerait le post en StateFailed.
			for i := 0; i < maxAttempts+3; i++ {
				w.drainOnce(ctx)
			}

			// L'orphelin n'a jamais été republié (statut inchangé).
			if got, _ := store.PublishStatus(ctx, id, tc.orphanNet); got != "publishing" {
				t.Fatalf("orphelin %s status = %q, attendu publishing (jamais republié)", tc.orphanNet, got)
			}
			// Le réseau en échec franc reste re-tentable ('failed'), pas verrouillé.
			if got, _ := store.PublishStatus(ctx, id, tc.failingNet); got != "failed" {
				t.Fatalf("réseau en échec %s status = %q, attendu failed (re-tentable)", tc.failingNet, got)
			}

			var state string
			var attempts int
			if err := db.QueryRow(`SELECT state, attempts FROM social_post_queue WHERE id=?`, id).Scan(&state, &attempts); err != nil {
				t.Fatalf("lecture file: %v", err)
			}
			if state != string(StatePending) {
				t.Fatalf("state = %q, attendu pending (parqué — ni sent ni failed tant qu'un orphelin subsiste)", state)
			}
			if attempts >= maxAttempts {
				t.Fatalf("attempts = %d, attendu < maxAttempts=%d (non épuisé : hasOrphan gèle l'incrément)", attempts, maxAttempts)
			}
		})
	}
}

// TestMarkPublished_DraftVsPublished prouve que le journal distingue un BROUILLON
// (PostRef.URL = DraftPendingURL, ex remise TikTok à valider à la main) d'une
// PUBLICATION PUBLIQUE (URL publique réelle) : publish_state vaut 'draft' dans le
// premier cas, 'published' dans le second. DB réelle (migrations chassis appliquées).
func TestMarkPublished_DraftVsPublished(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)

	for _, tc := range []struct {
		name    string
		network string
		ref     PostRef
		want    string
	}{
		{
			name:    "brouillon_tiktok",
			network: "tiktok",
			ref:     PostRef{Network: "tiktok", ExternalID: "publish-123", URL: DraftPendingURL},
			want:    PublishStateDraft,
		},
		{
			name:    "publication_publique",
			network: "facebook",
			ref:     PostRef{Network: "facebook", ExternalID: "ext-1", URL: "https://example.test/post/1"},
			want:    PublishStatePublished,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := store.Enqueue(ctx, SocialPost{
				Content:        "Remise " + tc.name,
				Networks:       []string{tc.network},
				IdempotencyKey: "draft-vs-pub-" + tc.name,
			})
			if err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if err := store.ClaimPublish(ctx, id, tc.network); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := store.MarkPublished(ctx, id, tc.ref); err != nil {
				t.Fatalf("mark published: %v", err)
			}
			var status, publishState string
			if err := db.QueryRow(
				`SELECT status, publish_state FROM social_post_log WHERE post_id=? AND network=?`,
				id, tc.network).Scan(&status, &publishState); err != nil {
				t.Fatalf("lecture journal: %v", err)
			}
			if status != "sent" {
				t.Fatalf("status = %q, attendu sent", status)
			}
			if publishState != tc.want {
				t.Fatalf("publish_state = %q, attendu %q", publishState, tc.want)
			}
		})
	}
}

// TestMigrationReapplied_NoError prouve que la migration 00039 est idempotente : un
// second chassis.Run (sentinel schema_version) ne rejoue pas l'ALTER TABLE et la
// colonne publish_state reste présente.
func TestMigrationReapplied_NoError(t *testing.T) {
	db := newTestDB(t) // chassis.Run déjà exécuté une fois par le helper
	if err := chassis.Run(db); err != nil {
		t.Fatalf("ré-application des migrations: %v", err)
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('social_post_log') WHERE name='publish_state'`).Scan(&n); err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	if n != 1 {
		t.Fatalf("colonne publish_state présente %d fois, attendu 1", n)
	}
}

func TestResolvePublishing_InvalidOutcome(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)
	if err := store.ResolvePublishing(ctx, "x", "facebook", PublishOutcome("bidon")); !errors.Is(err, ErrInvalidOutcome) {
		t.Fatalf("err = %v, attendu ErrInvalidOutcome", err)
	}
}

func TestEnqueueIdempotentKey(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	store := NewStore(db)

	id1, err := store.Enqueue(ctx, SocialPost{Networks: []string{"facebook"}, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	id2, err := store.Enqueue(ctx, SocialPost{Networks: []string{"facebook"}, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("double enqueue même clé → ids différents %q != %q", id1, id2)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM social_post_queue`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("count file = %d, attendu 1", n)
	}
}
