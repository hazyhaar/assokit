// Package api expose la surface publique stable d'assokit.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/bootstrap"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/handlers"
	"github.com/hazyhaar/assokit/internal/mailer"
	"github.com/hazyhaar/assokit/internal/types"
	appMiddleware "github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/static"
	"github.com/go-chi/chi/v5"

	_ "modernc.org/sqlite"
)

// App représente une instance assokit configurée et prête à servir.
type App struct {
	opts    Options
	db      *sql.DB
	srv     *http.Server
	handler http.Handler
	ml      *mailer.Mailer
	logger  *slog.Logger
}

// Options configure une nouvelle App.
type Options struct {
	// DBPath : chemin SQLite (créé/migré au New). ":memory:" accepté pour tests.
	DBPath string

	// BaseURL : URL publique du site (ex: "https://my-association.org").
	BaseURL string

	// Port : port HTTP d'écoute (ex: "8080").
	Port string

	// BrandingFS : système de fichiers contenant branding.toml + pages/*.md + assets.
	BrandingFS fs.FS

	// AdminEmail / AdminPassword : compte admin bootstrap (idempotent si users>0).
	AdminEmail    string
	AdminPassword string

	// ContactEmail : affiché sur le site / destinataire contact form. Défaut = AdminEmail.
	ContactEmail string

	// CookieSecret : 32+ bytes pour sessions ; généré aléatoire si vide (warn log).
	CookieSecret []byte

	// SMTP / Resend : optionnels ; si vides, mailer en mode "outbox enqueue only".
	SMTPHost     string
	SMTPUser     string
	SMTPPass     string
	SMTPPort     int
	SMTPFrom     string
	SMTPAdminTo  string
	ResendAPIKey string

	// Logger optionnel ; slog.Default() si nil.
	Logger *slog.Logger

	// LogLevel optionnel : niveau de log appliqué au handler créé si Logger==nil.
	// Permet à l'opérateur de passer Debug en investigation sans recompiler
	// (ex: ASSOKIT_LOG_LEVEL=debug). Défaut Info.
	// Ignoré si Logger non-nil (le caller contrôle son propre niveau).
	LogLevel slog.Level
}

// New crée une App, ouvre la DB, applique les migrations, charge le branding.
// Renvoie une erreur typée pour chaque échec (DB, migration, bootstrap, branding).
func New(opts Options) (*App, error) {
	logger := opts.Logger
	if logger == nil {
		level := opts.LogLevel
		if level == 0 {
			level = slog.LevelInfo
		}
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}

	// 1. DB open + ping.
	db, err := sql.Open("sqlite", opts.DBPath)
	if err != nil {
		return nil, fmt.Errorf("api.New: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: ping db: %w", err)
	}

	// 2. Migrations.
	if err := chassis.Run(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: migrations: %w", err)
	}

	// 3. Admin bootstrap (idempotent, skip si AdminEmail vide).
	if opts.AdminEmail != "" {
		if err := bootstrap.BootstrapAdmin(db, opts.AdminEmail, opts.AdminPassword, logger); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: bootstrap admin: %w", err)
		}
	}

	// 4. CookieSecret.
	secret := opts.CookieSecret
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: cookie secret rand: %w", err)
		}
		logger.Warn("CookieSecret non fourni — secret aléatoire généré : sessions invalides au prochain restart")
	} else if len(secret) < 32 {
		_ = db.Close()
		return nil, fmt.Errorf("api.New: CookieSecret trop court (%d bytes, minimum 32 requis)", len(secret))
	}

	// 5. Mailer init.
	from := opts.SMTPFrom
	if from == "" {
		from = opts.AdminEmail
	}
	adminTo := opts.SMTPAdminTo
	if adminTo == "" {
		adminTo = opts.AdminEmail
	}
	ml := &mailer.Mailer{
		DB:       db,
		APIKey:   opts.ResendAPIKey,
		SMTPHost: opts.SMTPHost,
		SMTPPort: opts.SMTPPort,
		SMTPUser: opts.SMTPUser,
		SMTPPass: opts.SMTPPass,
		From:     from,
		AdminTo:  adminTo,
		Logger:   logger,
	}
	if opts.SMTPHost == "" && opts.ResendAPIKey == "" {
		logger.Info("mailer: SMTP et Resend non configurés — mode outbox enqueue only")
	}

	// 6. Branding.
	if opts.BrandingFS != nil {
		b, err := theme.LoadFromFS(opts.BrandingFS, "branding.toml")
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("api.New: branding: %w", err)
		}
		theme.Init(b)
	} else {
		theme.Init(&theme.Branding{Name: "Assokit Default"})
	}

	// 7. AppDeps assemblage.
	contactEmail := opts.ContactEmail
	if contactEmail == "" {
		contactEmail = opts.AdminEmail
	}
	deps := app.AppDeps{
		DB:     db,
		Logger: logger,
		Config: config.Config{
			Port:         opts.Port,
			DBPath:       opts.DBPath,
			BaseURL:      opts.BaseURL,
			AdminEmail:   opts.AdminEmail,
			ContactEmail: contactEmail,
			CookieSecret: secret,
		},
		Mailer:     ml,
		BrandingFS: opts.BrandingFS,
		Profils:    defaultProfils(),
	}

	// 8. chi.Router + middlewares + routes.
	// RequestID monté EN PREMIER : tous les slogs en aval peuvent récupérer req_id via ctx.
	r := chi.NewRouter()
	r.Use(appMiddleware.RequestID)
	r.Use(chiMiddleware.Recoverer)
	r.Use(appMiddleware.CSRF(secret))
	r.Use(appMiddleware.Auth(db, secret))
	r.Use(appMiddleware.Flash)
	r.Use(appMiddleware.HTMX)
	handlers.MountRoutes(r, deps)
	r.Handle("/static/*", http.StripPrefix("/static", http.FileServer(http.FS(static.FS))))

	return &App{
		opts:    opts,
		db:      db,
		handler: r,
		ml:      ml,
		logger:  logger,
	}, nil
}

// ListenAndServe démarre le serveur HTTP. Bloquant. Honore ctx.Done() avec
// graceful shutdown timeout 10s.
func (a *App) ListenAndServe(ctx context.Context) error {
	// Mailer worker en background, s'arrête avec ctx.
	go a.ml.RunWorker(ctx)

	port := a.opts.Port
	if port == "" {
		port = "8080"
	}
	a.srv = &http.Server{
		Addr:              ":" + port,
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	select {
	case err := <-srvErr:
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.srv.Shutdown(shutCtx)
}

// Handler renvoie le chi.Router complet — utilisable en httptest.NewServer(app.Handler()).
func (a *App) Handler() http.Handler {
	return a.handler
}

// Shutdown arrête proprement le serveur HTTP et ferme la DB.
func (a *App) Shutdown(ctx context.Context) error {
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			return err
		}
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// defaultProfils retourne les profils d'inscription par défaut (Lot 3 : sourcés TOML).
func defaultProfils() []types.Profil {
	return []types.Profil{
		{Key: "individuel", Label: "Adhérent individuel"},
		{Key: "organisation", Label: "Organisation / Association"},
	}
}
