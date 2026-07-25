// Command yachts est la première instance verticale de la marketplace assokit :
// un silo « yachts » (A&C Yacht Brokers) servi par un front-office sobre
// (recherche + facettes + cartes + détail) monté sur le core assokit via le
// hook de bordure api.Options.RegisterRoutes — sans forker api.New.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/handlers"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/pkg/api"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/connectors/social"
	socialli "github.com/hazyhaar/assokit/pkg/connectors/social/linkedin"
	"github.com/hazyhaar/assokit/pkg/connectors/social/meta"
	socialtk "github.com/hazyhaar/assokit/pkg/connectors/social/tiktok"
	socialx "github.com/hazyhaar/assokit/pkg/connectors/social/x"
	socialyt "github.com/hazyhaar/assokit/pkg/connectors/social/youtube"
	"github.com/hazyhaar/assokit/pkg/listing"
	"github.com/hazyhaar/assokit/pkg/listingactions"
	svcrbac "github.com/hazyhaar/assokit/pkg/rbac"

	_ "modernc.org/sqlite"
)

func main() {
	addr := flag.String("addr", ":8090", "adresse d'écoute HTTP (ex. :8090)")
	siloPath := flag.String("silo", "config/silos/yachts.toml", "chemin du fichier de config silo TOML")
	dbPath := flag.String("db", "yachts.db", "chemin du fichier SQLite dédié au silo")
	appDB := flag.String("app-db", "assokit.db", "chemin de la DB core assokit (contient la table feedbacks)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(*addr, *siloPath, *dbPath, *appDB, logger); err != nil {
		logger.Error("yachts", "err", err)
		os.Exit(1)
	}
}

func run(addr, siloPath, dbPath, appDBPath string, logger *slog.Logger) error {
	silo, err := listing.LoadSilo(siloPath)
	if err != nil {
		return fmt.Errorf("chargement silo: %w", err)
	}

	// DB core assokit (contient la table feedbacks, migrations appliquées par api.New).
	// Ouverture en lecture concurrente (WAL) — handle distinct de celui de api.New.
	coreDB, err := sql.Open("sqlite", appDBPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return fmt.Errorf("ouverture db core %q: %w", appDBPath, err)
	}
	defer coreDB.Close()

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return fmt.Errorf("ouverture db silo %q: %w", dbPath, err)
	}
	defer db.Close()

	ctx := context.Background()
	store, err := listing.NewStore(ctx, db, silo)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	seeded, err := seedIfEmpty(ctx, store)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if seeded > 0 {
		logger.Info("inventaire d'amorçage inséré", "count", seeded, "silo", silo.ID)
	}

	h := &yachtsHandlers{store: store, silo: silo}
	fbh := &feedbackCommentsHandler{db: coreDB, appName: silo.ID, logger: logger}

	// Port pour le core (api.Options.Port) : extrait de -addr (":8090" → "8090").
	port := addr
	if len(port) > 0 && port[0] == ':' {
		port = port[1:]
	}

	// Connecteurs de publication sociaux construits AVANT api.New : le hook
	// RegisterRoutes (invoqué pendant api.New) câble les routes d'amorçage OAuth qui
	// consomment ces mêmes connecteurs. Meta est exclu de l'amorçage par route (son
	// jeton de page est pré-provisionné par procédure opérateur, cf. buildMetaNetworks).
	socialNetworks, err := buildMetaNetworks(coreDB, logger)
	if err != nil {
		return fmt.Errorf("câblage connecteur Meta: %w", err)
	}
	if err := addXNetwork(socialNetworks, coreDB, logger); err != nil {
		return fmt.Errorf("câblage connecteur X: %w", err)
	}
	if err := addYouTubeNetwork(socialNetworks, coreDB, logger); err != nil {
		return fmt.Errorf("câblage connecteur YouTube: %w", err)
	}
	if err := addTikTokNetwork(socialNetworks, coreDB, logger); err != nil {
		return fmt.Errorf("câblage connecteur TikTok: %w", err)
	}
	if err := addLinkedInNetwork(socialNetworks, coreDB, logger); err != nil {
		return fmt.Errorf("câblage connecteur LinkedIn: %w", err)
	}

	// Secret de cookie résolu à la BORDURE et partagé : il signe les cookies de
	// session du core (api.New) ET les cookies courts d'état OAuth des routes
	// d'amorçage social. Source canonique COOKIE_SECRET (hex), comme cmd/assokit ;
	// absent (dev), un secret aléatoire de process est généré (sessions invalidées au
	// restart, acceptable hors prod).
	cookieSecret, err := resolveCookieSecret()
	if err != nil {
		return fmt.Errorf("résolution COOKIE_SECRET: %w", err)
	}
	baseURL := os.Getenv("BASE_URL")
	cookieSecure := strings.HasPrefix(baseURL, "https://")

	app, err := api.New(api.Options{
		Port:         port,
		Logger:       logger,
		DBPath:       appDBPath,
		BaseURL:      baseURL,
		CookieSecret: cookieSecret,
		// Provider marketplace : expose les opérations de pkg/listing en Actions
		// (route HTTP admin + outil MCP + permission RBAC automatiques). L'identité
		// owner/buyer vient du contexte d'authentification, jamais des params.
		RegisterActions: listingactions.Register(store),
		RegisterRoutes: func(r chi.Router) error {
			r.Get("/annonces", h.handleIndex)
			r.Get("/annonces/fragment", h.handleFragment)
			r.Get("/annonces/{id}", h.handleDetail)
			// Endpoint de lecture pour le worker horos55 feedback-pull (PULL, pas d'auth).
			r.Get("/feedback/comments", fbh.ServeHTTP)
			// Routes d'amorçage OAuth des comptes de publication (X, TikTok, YouTube,
			// LinkedIn) : initiation + rappel, gardées par requireAdmin. État porté par
			// cookie court signé, jamais en DB. Meta exclu (procédure opérateur).
			handlers.MountSocialConnect(r, cookieSecret, cookieSecure, logger, socialConnectAdapters(socialNetworks)...)
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("api.New: %w", err)
	}

	// Accorder les permissions marketplace au grade membre système : publier/
	// éditer sa propre annonce, contacter un vendeur et rechercher sont des gestes
	// de membre, pas d'administration. Idempotent. La bordure (cmd) est le lieu
	// légitime du câblage RBAC ; le core n'accorde par défaut qu'à sys-admin.
	if err := grantListingPermsToMember(ctx, coreDB); err != nil {
		logger.Warn("octroi perms marketplace au grade membre", "err", err)
	}
	// La perm de modération va à un grade ÉLEVÉ (sys-moderator), jamais au membre :
	// masquer/restaurer une annonce est un geste d'autorité, pas un geste de membre.
	if err := grantModeratePermsToModerator(ctx, coreDB); err != nil {
		logger.Warn("octroi perm modération au grade modérateur", "err", err)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Worker d'alerte : livre les alertes de recherche sauvée à la publication d'une
	// annonce (matching MatchingSavedSearches + mailer à la bordure). Découplé du
	// cœur ; idempotent via la table sent_alerts.
	mlr := &mailer.Mailer{
		DB:       coreDB,
		APIKey:   os.Getenv("ASSOKIT_RESEND_API_KEY"),
		SMTPHost: os.Getenv("ASSOKIT_SMTP_HOST"),
		SMTPUser: os.Getenv("ASSOKIT_SMTP_USER"),
		SMTPPass: os.Getenv("ASSOKIT_SMTP_PASS"),
		From:     os.Getenv("ASSOKIT_MAIL_FROM"),
		Logger:   logger,
	}
	alerts, err := newAlertWorker(ctx, store, mlr, coreDB, silo.ID, logger)
	if err != nil {
		return fmt.Errorf("init worker alertes: %w", err)
	}
	go alerts.run(sigCtx, time.Minute)

	// Worker de publication réseaux sociaux. Draine la file locale social_post_queue
	// (migrée par api.New sur la DB cœur) et publie chaque réseau cible via son impl
	// Network. La carte socialNetworks a été construite plus haut (avant api.New, pour
	// que les routes d'amorçage OAuth la partagent). Single-writer : ce worker est le
	// seul écrivain des tables social.
	socialWorker := social.NewWorker(social.NewStore(coreDB), socialNetworks, time.Minute, logger)
	go socialWorker.Run(sigCtx)

	errChan := make(chan error, 1)
	go func() {
		logger.Info("démarrage instance yachts", "addr", addr, "silo", silo.ID)
		if err := app.ListenAndServe(sigCtx); err != nil {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-sigCtx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return app.Shutdown(shutCtx)
	}
}

// buildMetaNetworks construit les connecteurs Meta (Instagram et/ou Facebook) selon
// la configuration d'instance lue à la BORDURE (variables d'environnement, lieu
// légitime — le package meta, lui, ne lit jamais l'environnement). Une instance qui
// ne configure aucune surface Meta (ni META_IG_USER_ID ni META_PAGE_ID) obtient une
// carte vide, sans erreur : la publication sociale est optionnelle. Si une surface
// est demandée mais que le coffre (ASSOKIT_MASTER_KEY) est absent, le câblage est
// fail-loud : un connecteur incapable de lire son Page Access Token ne doit pas
// démarrer silencieusement.
func buildMetaNetworks(coreDB *sql.DB, logger *slog.Logger) (map[string]social.Network, error) {
	igUserID := os.Getenv("META_IG_USER_ID")
	pageID := os.Getenv("META_PAGE_ID")
	if igUserID == "" && pageID == "" {
		return map[string]social.Network{}, nil
	}
	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	if masterKey == "" {
		return nil, fmt.Errorf("META_* configuré mais ASSOKIT_MASTER_KEY absent (coffre requis pour le Page Access Token)")
	}
	vault, err := assets.NewVault(coreDB, masterKey)
	if err != nil {
		return nil, fmt.Errorf("coffre Meta: %w", err)
	}
	appID := os.Getenv("META_APP_ID")

	nets := map[string]social.Network{}
	if igUserID != "" {
		c, err := meta.New(vault, meta.Config{Surface: meta.SurfaceInstagram, TargetID: igUserID, AppID: appID}, nil)
		if err != nil {
			return nil, err
		}
		nets[c.ID()] = c
	}
	if pageID != "" {
		c, err := meta.New(vault, meta.Config{Surface: meta.SurfaceFacebook, TargetID: pageID, AppID: appID}, nil)
		if err != nil {
			return nil, err
		}
		nets[c.ID()] = c
	}
	logger.Info("connecteur Meta câblé", "surfaces", len(nets))
	return nets, nil
}

// addXNetwork ajoute le connecteur X (Twitter) à la carte des réseaux si l'instance
// l'active (X_ENABLED non vide), en lisant la configuration à la BORDURE (variables
// d'environnement, lieu légitime — le package x ne lit jamais l'environnement). Une
// instance qui n'active pas X laisse la carte inchangée, sans erreur : la publication
// X est optionnelle. Si X est activé mais que le coffre (ASSOKIT_MASTER_KEY) est
// absent, le câblage est fail-loud : un connecteur incapable de lire son jeton d'accès
// ne doit pas démarrer silencieusement.
func addXNetwork(nets map[string]social.Network, coreDB *sql.DB, logger *slog.Logger) error {
	if os.Getenv("X_ENABLED") == "" {
		return nil
	}
	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	if masterKey == "" {
		return fmt.Errorf("X_ENABLED défini mais ASSOKIT_MASTER_KEY absent (coffre requis pour le jeton d'accès)")
	}
	vault, err := assets.NewVault(coreDB, masterKey)
	if err != nil {
		return fmt.Errorf("coffre X: %w", err)
	}
	c, err := socialx.New(vault, socialx.Config{RedirectURI: os.Getenv("X_REDIRECT_URI")}, nil)
	if err != nil {
		return err
	}
	nets[c.ID()] = c
	logger.Info("connecteur X câblé", "network", c.ID())
	return nil
}

// addYouTubeNetwork ajoute le connecteur YouTube à la carte des réseaux si l'instance
// l'active (YOUTUBE_ENABLED non vide), en lisant la configuration à la BORDURE (variables
// d'environnement, lieu légitime — le package youtube ne lit jamais l'environnement). Une
// instance qui n'active pas YouTube laisse la carte inchangée, sans erreur : la
// publication YouTube est optionnelle. Si YouTube est activé mais que le coffre
// (ASSOKIT_MASTER_KEY) est absent, le câblage est fail-loud : un connecteur incapable de
// lire son jeton d'accès ne doit pas démarrer silencieusement.
func addYouTubeNetwork(nets map[string]social.Network, coreDB *sql.DB, logger *slog.Logger) error {
	if os.Getenv("YOUTUBE_ENABLED") == "" {
		return nil
	}
	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	if masterKey == "" {
		return fmt.Errorf("YOUTUBE_ENABLED défini mais ASSOKIT_MASTER_KEY absent (coffre requis pour le jeton d'accès)")
	}
	vault, err := assets.NewVault(coreDB, masterKey)
	if err != nil {
		return fmt.Errorf("coffre YouTube: %w", err)
	}
	c, err := socialyt.New(vault, socialyt.Config{RedirectURI: os.Getenv("YOUTUBE_REDIRECT_URI")}, nil)
	if err != nil {
		return err
	}
	nets[c.ID()] = c
	logger.Info("connecteur YouTube câblé", "network", c.ID())
	return nil
}

// addTikTokNetwork ajoute le connecteur TikTok à la carte des réseaux si l'instance
// l'active (TIKTOK_ENABLED non vide), en lisant la configuration à la BORDURE (variables
// d'environnement, lieu légitime — le package tiktok ne lit jamais l'environnement). Une
// instance qui n'active pas TikTok laisse la carte inchangée, sans erreur : la
// publication TikTok est optionnelle. Si TikTok est activé mais que le coffre
// (ASSOKIT_MASTER_KEY) est absent, le câblage est fail-loud : un connecteur incapable de
// lire son jeton d'accès ne doit pas démarrer silencieusement. Rappel : TikTok dépose un
// brouillon en boîte de réception, à valider manuellement (modèle brouillon-en-boîte).
func addTikTokNetwork(nets map[string]social.Network, coreDB *sql.DB, logger *slog.Logger) error {
	if os.Getenv("TIKTOK_ENABLED") == "" {
		return nil
	}
	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	if masterKey == "" {
		return fmt.Errorf("TIKTOK_ENABLED défini mais ASSOKIT_MASTER_KEY absent (coffre requis pour le jeton d'accès)")
	}
	vault, err := assets.NewVault(coreDB, masterKey)
	if err != nil {
		return fmt.Errorf("coffre TikTok: %w", err)
	}
	c, err := socialtk.New(vault, socialtk.Config{RedirectURI: os.Getenv("TIKTOK_REDIRECT_URI")}, nil)
	if err != nil {
		return err
	}
	nets[c.ID()] = c
	logger.Info("connecteur TikTok câblé", "network", c.ID())
	return nil
}

// addLinkedInNetwork ajoute le connecteur LinkedIn à la carte des réseaux si l'instance
// l'active (LINKEDIN_ENABLED non vide), en lisant la configuration à la BORDURE (variables
// d'environnement, lieu légitime — le package linkedin ne lit jamais l'environnement). Une
// instance qui n'active pas LinkedIn laisse la carte inchangée, sans erreur : la
// publication LinkedIn est optionnelle. Si LinkedIn est activé mais que le coffre
// (ASSOKIT_MASTER_KEY) est absent, le câblage est fail-loud : un connecteur incapable de
// lire son jeton d'accès ne doit pas démarrer silencieusement. LINKEDIN_AUTHOR_URN porte
// l'auteur des posts (urn:li:organization:… — habilitation Partner Program requise — ou
// urn:li:person:…).
func addLinkedInNetwork(nets map[string]social.Network, coreDB *sql.DB, logger *slog.Logger) error {
	if os.Getenv("LINKEDIN_ENABLED") == "" {
		return nil
	}
	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	if masterKey == "" {
		return fmt.Errorf("LINKEDIN_ENABLED défini mais ASSOKIT_MASTER_KEY absent (coffre requis pour le jeton d'accès)")
	}
	vault, err := assets.NewVault(coreDB, masterKey)
	if err != nil {
		return fmt.Errorf("coffre LinkedIn: %w", err)
	}
	c, err := socialli.New(vault, socialli.Config{
		AuthorURN:   os.Getenv("LINKEDIN_AUTHOR_URN"),
		RedirectURI: os.Getenv("LINKEDIN_REDIRECT_URI"),
	}, nil)
	if err != nil {
		return err
	}
	nets[c.ID()] = c
	logger.Info("connecteur LinkedIn câblé", "network", c.ID())
	return nil
}

// pkceOAuth est l'ensemble des méthodes OAuth+PKCE (X, TikTok) requises par les routes
// d'amorçage. Interface définie côté consommateur (bordure), jamais côté connecteur.
type pkceOAuth interface {
	AuthorizeURL(ctx context.Context, state, verifier string) (string, error)
	ExchangeCode(ctx context.Context, code, verifier string) (string, error)
}

// plainOAuth est l'ensemble des méthodes OAuth sans PKCE (YouTube, LinkedIn).
type plainOAuth interface {
	AuthorizeURL(ctx context.Context, state string) (string, error)
	ExchangeCode(ctx context.Context, code string) (string, error)
}

// socialConnectAdapters dérive les adaptateurs d'amorçage OAuth des connecteurs déjà
// construits. Un réseau qui n'expose pas le flux code d'autorisation (Meta : seul
// RefreshLongLivedToken) n'implémente aucune des deux interfaces et est ignoré.
func socialConnectAdapters(nets map[string]social.Network) []handlers.SocialConnector {
	var out []handlers.SocialConnector
	for id, n := range nets {
		switch c := n.(type) {
		case pkceOAuth:
			out = append(out, handlers.SocialConnector{
				Network:   id,
				UsesPKCE:  true,
				Authorize: c.AuthorizeURL,
				Exchange:  c.ExchangeCode,
			})
		case plainOAuth:
			pc := c
			out = append(out, handlers.SocialConnector{
				Network:   id,
				UsesPKCE:  false,
				Authorize: func(ctx context.Context, state, _ string) (string, error) { return pc.AuthorizeURL(ctx, state) },
				Exchange:  func(ctx context.Context, code, _ string) (string, error) { return pc.ExchangeCode(ctx, code) },
			})
		}
	}
	return out
}

// resolveCookieSecret lit COOKIE_SECRET (hex, source canonique partagée avec
// cmd/assokit) ; à défaut, génère un secret aléatoire de process (32 octets). Hors prod
// uniquement : un restart invalide alors les sessions et les états OAuth en vol.
func resolveCookieSecret() ([]byte, error) {
	if s := os.Getenv("COOKIE_SECRET"); s != "" {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("COOKIE_SECRET: décodage hex: %w", err)
		}
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("génération secret aléatoire: %w", err)
	}
	return b, nil
}

// grantListingPermsToMember s'assure que les permissions marketplace existent et
// sont accordées au grade système 'sys-member'. Idempotent (EnsurePermission +
// GrantPerm sur INSERT OR IGNORE). Appelé après api.New, qui a appliqué les
// migrations RBAC sur la DB core.
func grantListingPermsToMember(ctx context.Context, coreDB *sql.DB) error {
	store := &svcrbac.Store{DB: coreDB}
	for _, perm := range listingactions.Perms() {
		permID, err := store.EnsurePermission(ctx, perm, "Permission marketplace "+perm)
		if err != nil {
			return fmt.Errorf("ensure perm %q: %w", perm, err)
		}
		if err := store.GrantPerm(ctx, "sys-member", permID); err != nil {
			return fmt.Errorf("grant perm %q au grade membre: %w", perm, err)
		}
	}
	return nil
}

// grantModeratePermsToModerator accorde les permissions de modération marketplace
// au grade système 'sys-moderator' UNIQUEMENT — jamais à sys-member. Masquer ou
// restaurer une annonce est un geste d'autorité ; un membre ordinaire en est
// exclu par la permission requise. Idempotent.
func grantModeratePermsToModerator(ctx context.Context, coreDB *sql.DB) error {
	store := &svcrbac.Store{DB: coreDB}
	for _, perm := range listingactions.ModeratorPerms() {
		permID, err := store.EnsurePermission(ctx, perm, "Permission modération marketplace "+perm)
		if err != nil {
			return fmt.Errorf("ensure perm %q: %w", perm, err)
		}
		if err := store.GrantPerm(ctx, "sys-moderator", permID); err != nil {
			return fmt.Errorf("grant perm %q au grade modérateur: %w", perm, err)
		}
	}
	return nil
}
