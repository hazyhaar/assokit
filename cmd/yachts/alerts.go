package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/hazyhaar/assokit/pkg/listing"
)

// alertMailer est l'interface minimale consommée par le worker d'alerte : la
// seule capacité requise du mailer est de mettre un email en file. Définie côté
// consommateur pour rester stubbable en test (un faux mailer enregistre les
// envois sans SMTP). *mailer.Mailer la satisfait.
type alertMailer interface {
	Enqueue(ctx context.Context, to, subject, bodyText, bodyHTML string) error
}

// alertEmailLookup résout l'adresse email d'un utilisateur (propriétaire d'une
// recherche sauvée) depuis la DB core. Défini côté consommateur ; en production
// c'est un SELECT sur la table users.
type alertEmailLookup func(ctx context.Context, userID string) (string, bool, error)

// savedSearchMatcher est la primitive de matching de pkg/listing (P2) que le
// worker consomme. *listing.Store la satisfait via MatchingSavedSearches.
type savedSearchMatcher interface {
	Search(ctx context.Context, f listing.Filter) ([]listing.Listing, error)
	MatchingSavedSearches(ctx context.Context, l *listing.Listing) ([]listing.SavedSearch, error)
}

// alertWorker livre les alertes de recherche sauvée à la PUBLICATION d'une
// annonce. Découplé du cœur : pkg/listing n'importe ni le mailer ni ce worker.
//
// Idempotence : la table sent_alerts mémorise chaque paire (listing, recherche)
// déjà notifiée. Le worker scanne les annonces publiées (Search exclut déjà les
// masquées), rapproche les recherches sauvées matchantes, et n'enqueue que les
// paires non encore notifiées — une re-publication ou un re-run ne ré-envoie
// jamais.
type alertWorker struct {
	matcher savedSearchMatcher
	mailer  alertMailer
	coreDB  *sql.DB
	lookup  alertEmailLookup
	siloID  string
	logger  *slog.Logger
}

// newAlertWorker câble le worker sur le store silo et la DB core, et crée la
// table d'idempotence. lookup résout l'email d'un user depuis la DB core.
func newAlertWorker(ctx context.Context, matcher savedSearchMatcher, m alertMailer, coreDB *sql.DB, siloID string, logger *slog.Logger) (*alertWorker, error) {
	w := &alertWorker{
		matcher: matcher,
		mailer:  m,
		coreDB:  coreDB,
		lookup:  defaultEmailLookup(coreDB),
		siloID:  siloID,
		logger:  logger,
	}
	if err := w.migrate(ctx); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *alertWorker) migrate(ctx context.Context) error {
	_, err := w.coreDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sent_alerts (
		silo            TEXT NOT NULL,
		listing_id      TEXT NOT NULL,
		saved_search_id TEXT NOT NULL,
		created_at      TEXT NOT NULL,
		PRIMARY KEY (silo, listing_id, saved_search_id)
	)`)
	if err != nil {
		return fmt.Errorf("alertWorker.migrate sent_alerts: %w", err)
	}
	return nil
}

// defaultEmailLookup résout l'email d'un utilisateur depuis la table users de la
// DB core. Un user introuvable retourne ok=false (pas d'erreur) — l'alerte est
// simplement sautée.
func defaultEmailLookup(coreDB *sql.DB) alertEmailLookup {
	return func(ctx context.Context, userID string) (string, bool, error) {
		var email string
		err := coreDB.QueryRowContext(ctx,
			`SELECT email FROM users WHERE id = ?`, userID).Scan(&email)
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("defaultEmailLookup: %w", err)
		}
		return email, true, nil
	}
}

// scanOnce rapproche les annonces publiées des recherches sauvées et enqueue les
// alertes nouvelles (idempotent via sent_alerts). Retourne le nombre d'alertes
// effectivement émises lors de ce passage.
func (w *alertWorker) scanOnce(ctx context.Context) (int, error) {
	// Search exclut déjà les annonces masquées (modération) : on n'alerte jamais
	// sur une annonce masquée.
	published, err := w.matcher.Search(ctx, listing.Filter{
		Status: listing.StatusPublished,
		Limit:  1000,
	})
	if err != nil {
		return 0, fmt.Errorf("alertWorker.scanOnce search: %w", err)
	}
	sent := 0
	for i := range published {
		l := published[i]
		matches, err := w.matcher.MatchingSavedSearches(ctx, &l)
		if err != nil {
			return sent, fmt.Errorf("alertWorker.scanOnce matching %s: %w", l.ID, err)
		}
		for _, ss := range matches {
			emitted, err := w.deliver(ctx, &l, ss)
			if err != nil {
				return sent, err
			}
			if emitted {
				sent++
			}
		}
	}
	return sent, nil
}

// deliver enqueue une alerte pour une paire (annonce, recherche) si elle n'a pas
// déjà été notifiée. Retourne true si un email a été mis en file. La marque
// d'idempotence est posée APRÈS l'enqueue réussi : un échec d'enqueue laisse la
// paire ré-essayable au prochain passage.
func (w *alertWorker) deliver(ctx context.Context, l *listing.Listing, ss listing.SavedSearch) (bool, error) {
	already, err := w.alreadySent(ctx, l.ID, ss.ID)
	if err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	email, ok, err := w.lookup(ctx, ss.UserID)
	if err != nil {
		return false, err
	}
	if !ok || email == "" {
		// Owner sans email résolvable : on marque la paire comme traitée pour ne pas
		// reboucler indéfiniment, et on saute l'envoi.
		return false, w.markSent(ctx, l.ID, ss.ID)
	}
	subject := fmt.Sprintf("Nouvelle annonce pour votre recherche « %s »", ss.Name)
	bodyText := fmt.Sprintf(
		"Une nouvelle annonce correspond à votre recherche sauvegardée « %s » :\n\n%s\n",
		ss.Name, l.Title)
	if err := w.mailer.Enqueue(ctx, email, subject, bodyText, ""); err != nil {
		return false, fmt.Errorf("alertWorker.deliver enqueue: %w", err)
	}
	if err := w.markSent(ctx, l.ID, ss.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (w *alertWorker) alreadySent(ctx context.Context, listingID, savedSearchID string) (bool, error) {
	var one int
	err := w.coreDB.QueryRowContext(ctx,
		`SELECT 1 FROM sent_alerts WHERE silo=? AND listing_id=? AND saved_search_id=?`,
		w.siloID, listingID, savedSearchID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("alertWorker.alreadySent: %w", err)
	}
	return true, nil
}

func (w *alertWorker) markSent(ctx context.Context, listingID, savedSearchID string) error {
	_, err := w.coreDB.ExecContext(ctx,
		`INSERT OR IGNORE INTO sent_alerts (silo, listing_id, saved_search_id, created_at)
		 VALUES (?, ?, ?, ?)`,
		w.siloID, listingID, savedSearchID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("alertWorker.markSent: %w", err)
	}
	return nil
}

// run lance la boucle de scan périodique jusqu'à annulation du contexte. Un
// premier passage est exécuté immédiatement.
func (w *alertWorker) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if n, err := w.scanOnce(ctx); err != nil {
			w.logger.Warn("scan alertes recherches sauvées", "err", err)
		} else if n > 0 {
			w.logger.Info("alertes recherches sauvées émises", "count", n, "silo", w.siloID)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
