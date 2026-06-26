// CLAUDE:SUMMARY Rétention TTL des transcripts : purge les enregistrements trop anciens.
// Pattern ticker identique à internal/mailer/mailer.go:RunWorker.
package transcription

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// RetentionStore est l'interface côté consommateur pour la purge TTL.
type RetentionStore interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}

// RetentionWorker purge périodiquement les anciens transcripts.
type RetentionWorker struct {
	Store  RetentionStore
	Logger *slog.Logger
	// TTL : durée de rétention. Lire depuis ASSOKIT_TRANSCRIPT_TTL_DAYS (défaut 90).
	TTL time.Duration
	// Tick : intervalle de vérification (défaut 24h).
	Tick time.Duration
}

// NewRetentionWorker construit un RetentionWorker avec les valeurs d'env par défaut.
func NewRetentionWorker(store RetentionStore, logger *slog.Logger) *RetentionWorker {
	days := 90
	if s := os.Getenv("ASSOKIT_TRANSCRIPT_TTL_DAYS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			days = n
		}
	}
	return &RetentionWorker{
		Store:  store,
		Logger: logger,
		TTL:    time.Duration(days) * 24 * time.Hour,
		Tick:   24 * time.Hour,
	}
}

func (w *RetentionWorker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

// PurgeOldTranscripts supprime les transcripts dont started_at < now-ttl.
// Retourne le nombre de lignes supprimées.
func (w *RetentionWorker) PurgeOldTranscripts(ctx context.Context, ttl time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-ttl)
	n, err := w.Store.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		w.logger().Info("retention: transcripts purgés", "count", n, "cutoff", cutoff.Format(time.RFC3339))
	}
	return n, nil
}

// RunWorker démarre la boucle de purge TTL. Ticker toutes les w.Tick.
// Honore ctx.Done() pour un arrêt propre. Modèle : internal/mailer/mailer.go:RunWorker.
func (w *RetentionWorker) RunWorker(ctx context.Context) {
	ticker := time.NewTicker(w.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.PurgeOldTranscripts(ctx, w.TTL); err != nil {
				w.logger().Error("retention: erreur purge", "err", err)
			}
		}
	}
}
