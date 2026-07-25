// CLAUDE:SUMMARY Worker à ticker drainant social_post_queue et publiant chaque
// réseau cible via son impl Network ; idempotence par (post, réseau), retries bornés.
// CLAUDE:WARN Single-writer : un seul worker écrit les tables social. Modèle calqué
// sur internal/mailer.RunWorker (ticker + drain + ctx.Done) et l'idempotence du
// worker d'alertes (cmd/yachts). Aucune planification via jobs.db horos55.
package social

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// defaultInterval est la cadence de drain de la file.
	defaultInterval = 30 * time.Second
	// drainBatch borne le nombre de posts traités par tick.
	drainBatch = 20
	// maxAttempts borne les re-tentatives d'un post avant abandon (state=failed).
	maxAttempts = 5
)

// Worker draine la file de publication et publie sur les réseaux enregistrés.
type Worker struct {
	store    *Store
	networks map[string]Network
	interval time.Duration
	logger   *slog.Logger
}

// NewWorker construit un worker. networks associe un id de réseau à son impl.
// interval <= 0 retombe sur defaultInterval. Un logger nil retombe sur slog.Default.
func NewWorker(store *Store, networks map[string]Network, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = defaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	nets := make(map[string]Network, len(networks))
	for id, n := range networks {
		nets[id] = n
	}
	return &Worker{store: store, networks: nets, interval: interval, logger: logger}
}

// Run lance la boucle de drain jusqu'à annulation du contexte. Un premier passage
// est exécuté immédiatement (modèle mailer.RunWorker).
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("worker social démarré", "interval", w.interval.String())
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.drainOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drainOnce(ctx)
		}
	}
}

// drainOnce traite un lot de posts dus. Erreur de lecture = warn + abandon du tick.
func (w *Worker) drainOnce(ctx context.Context) {
	posts, err := w.store.DuePosts(ctx, time.Now().UTC(), drainBatch)
	if err != nil {
		w.logger.Error("worker social: lecture file", "err", err)
		return
	}
	for i := range posts {
		w.publishPost(ctx, posts[i])
	}
}

// publishPost publie un post sur chacun de ses réseaux cibles, idempotemment.
//
// Sémantique AT-MOST-ONCE (soldée en W1, connecteur Meta réel) : la marque
// 'publishing' est posée AVANT l'appel Publish et VERROUILLE la republication. Le
// worker DISTINGUE désormais (via Store.PublishStatus) deux verrous très différents :
//   - 'sent' : la diffusion est réellement faite. Le réseau est terminal et n'empêche
//     pas le post d'être promu StateSent.
//   - 'publishing' : ORPHELIN. Une diffusion est partie en vol (issue inconnue,
//     typiquement un crash entre le Publish réussi côté provider et MarkPublished). On
//     NE republie PAS (pas de doublon) et — point clé du correctif — on ne re-tente
//     PAS vers l'échec : le post est PARQUÉ en pending SANS incrément d'attempts, en
//     attente d'une réconciliation manuelle (action social.reconcile → ResolvePublishing).
//
// Un échec réseau franc reste inscrit 'failed' (re-tentable) et déclenche la logique de
// retry bornée habituelle (attempts → maxAttempts → StateFailed). Un post dont les
// SEULS réseaux non terminaux sont des orphelins 'publishing' n'est jamais poussé en
// StateFailed par l'incrément de tentatives : il resterait sinon re-tenté en boucle alors
// qu'il est peut-être déjà diffusé. Aucun panic : warn + continue.
func (w *Worker) publishPost(ctx context.Context, p SocialPost) {
	allSent := true
	hasOrphan := false
	frankFailure := false
	var lastErr string

	for _, netID := range p.Networks {
		status, err := w.store.PublishStatus(ctx, p.ID, netID)
		if err != nil {
			w.logger.Warn("worker social: vérif idempotence", "post", p.ID, "network", netID, "err", err)
			allSent = false
			frankFailure = true
			lastErr = err.Error()
			continue
		}
		switch status {
		case "sent":
			// Réseau réellement diffusé : terminal, n'empêche pas la promotion StateSent.
			continue
		case "publishing":
			// Orphelin : diffusion en vol, issue inconnue. Ni republiée (at-most-once),
			// ni re-tentée vers l'échec. Le post est parqué en attente de réconciliation.
			w.logger.Warn("worker social: diffusion orpheline 'publishing', post parqué en attente de réconciliation", "post", p.ID, "network", netID)
			allSent = false
			hasOrphan = true
			continue
		}
		// status "" (jamais tenté) ou "failed" (échec re-tentable) → (re)tentative.

		net, ok := w.networks[netID]
		if !ok {
			// Réseau cible sans impl enregistrée (ex G0 sans connecteur réel) : on
			// n'échoue pas le post, on le laisse en attente d'un worker mieux câblé.
			w.logger.Warn("worker social: réseau non enregistré, post différé", "post", p.ID, "network", netID)
			allSent = false
			frankFailure = true
			lastErr = "réseau non enregistré: " + netID
			continue
		}

		// Marque d'idempotence posée AVANT l'appel Publish.
		if err := w.store.ClaimPublish(ctx, p.ID, netID); err != nil {
			w.logger.Warn("worker social: claim", "post", p.ID, "network", netID, "err", err)
			allSent = false
			frankFailure = true
			lastErr = err.Error()
			continue
		}

		ref, err := net.Publish(ctx, p)
		if err != nil {
			lastErr = err.Error()
			allSent = false
			frankFailure = true
			if e := w.store.MarkPublishFailed(ctx, p.ID, netID, lastErr); e != nil {
				w.logger.Warn("worker social: journal échec", "post", p.ID, "network", netID, "err", e)
			}
			w.logger.Warn("worker social: publication échouée", "post", p.ID, "network", netID, "err", err)
			continue
		}
		if ref.Network == "" {
			ref.Network = netID
		}
		if err := w.store.MarkPublished(ctx, p.ID, ref); err != nil {
			w.logger.Warn("worker social: journal succès", "post", p.ID, "network", netID, "err", err)
			allSent = false
			frankFailure = true
			lastErr = err.Error()
		} else if publishStateFor(ref) == PublishStateDraft {
			// Remise acceptée par le provider mais NON publiée : un brouillon attend une
			// validation manuelle (ex TikTok). Le réseau compte comme terminal pour la
			// promotion StateSent — la remise a réussi — mais l'opérateur doit le savoir.
			w.logger.Info("worker social: brouillon déposé chez le provider, publication à valider manuellement", "post", p.ID, "network", netID)
		}
	}

	switch {
	case allSent:
		if err := w.store.MarkPostState(ctx, p.ID, StateSent, p.Attempts, ""); err != nil {
			w.logger.Warn("worker social: état post", "post", p.ID, "err", err)
		}
	case hasOrphan:
		// PARCAGE — PRIME sur frankFailure (invariant C2 : JAMAIS StateFailed tant qu'une
		// ligne 'publishing' subsiste). Dès qu'un réseau reste orphelin, le post est parqué
		// en pending SANS incrément d'attempts, MÊME si un autre réseau a échoué franchement :
		// incrémenter pousserait l'agrégat vers StateFailed alors qu'une diffusion peut-être
		// déjà live attend confirmation (un opérateur verrait 'failed', relancerait avec une
		// nouvelle clé → re-post réel de l'orphelin ; pire, StateFailed n'est plus drainé par
		// DuePosts, donc réconcilier ne re-promouvrait jamais le post). Le réseau en échec
		// franc reste inscrit 'failed' (re-tentable au tick suivant) mais ne fait pas vieillir
		// le compteur tant que l'orphelin n'est pas tranché (action social.reconcile →
		// ResolvePublishing : 'failed' libère la republication, 'sent' lève le blocage).
		note := "diffusion(s) orpheline(s) 'publishing' en attente de réconciliation"
		if frankFailure {
			note = "orphelin 'publishing' + échec franc coexistants : parqué (échec re-tentable, agrégat gelé hors StateFailed)"
		}
		if err := w.store.MarkPostState(ctx, p.ID, StatePending, p.Attempts, note); err != nil {
			w.logger.Warn("worker social: état post", "post", p.ID, "err", err)
		}
	case frankFailure:
		// Erreur franche SANS orphelin : logique de retry bornée habituelle.
		if p.Attempts+1 >= maxAttempts {
			if err := w.store.MarkPostState(ctx, p.ID, StateFailed, p.Attempts+1, lastErr); err != nil {
				w.logger.Warn("worker social: état post", "post", p.ID, "err", err)
			}
			w.logger.Error("worker social: post abandonné après retries", "post", p.ID, "attempts", p.Attempts+1)
		} else if err := w.store.MarkPostState(ctx, p.ID, StatePending, p.Attempts+1, lastErr); err != nil {
			w.logger.Warn("worker social: état post", "post", p.ID, "err", err)
		}
	default:
		// Cas résiduel (aucun réseau cible terminal sans succès, sans erreur, sans
		// orphelin) : rester pending sans incrément, jamais d'abandon silencieux.
		if err := w.store.MarkPostState(ctx, p.ID, StatePending, p.Attempts, lastErr); err != nil {
			w.logger.Warn("worker social: état post", "post", p.ID, "err", err)
		}
	}
}

// ErrNoNetworks signale un worker démarré sans aucune impl réseau (diagnostic).
var ErrNoNetworks = errors.New("social: aucun réseau enregistré")
