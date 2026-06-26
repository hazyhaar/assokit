package main

import (
	"context"
	"encoding/hex"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/hazyhaar/assokit/pkg/api"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "assokit.db"
	}
	brandingDir := os.Getenv("BRANDING_DIR")
	if brandingDir == "" {
		brandingDir = "./config"
	}
	// ./config robuste : si le branding.toml est absent, on démarre quand même
	// avec le branding par défaut (BrandingFS nil) au lieu d'échouer au boot.
	// Permet un premier lancement sans configuration préalable.
	var brandingFS fs.FS
	if _, err := os.Stat(filepath.Join(brandingDir, "branding.toml")); err == nil {
		brandingFS = os.DirFS(brandingDir)
	} else {
		slog.Warn("branding.toml introuvable — démarrage avec le branding par défaut",
			"dir", brandingDir, "err", err)
	}

	var cookieSecret []byte
	if s := os.Getenv("COOKIE_SECRET"); s != "" {
		b, err := hex.DecodeString(s)
		if err != nil {
			log.Fatalf("COOKIE_SECRET: hex decode: %v", err)
		}
		cookieSecret = b
	}

	smtpPort, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))

	// Mode prod : la bordure décide (le core ne lit pas l'environnement).
	// En prod, un CookieSecret absent est fatal (cf. api.New).
	prod := os.Getenv("APP_ENV") == "production"

	// Interface d'écoute : BIND_ADDR explicite si fourni ; sinon, derrière un
	// reverse-proxy de confiance (TRUST_PROXY_HEADERS=true), on se restreint au
	// loopback par défaut — le proxy est le seul point d'entrée légitime. Sans
	// proxy déclaré, on garde l'écoute sur toutes les interfaces (défaut historique).
	bindHost := os.Getenv("BIND_ADDR")
	if bindHost == "" && os.Getenv("TRUST_PROXY_HEADERS") == "true" {
		bindHost = "127.0.0.1"
	}

	opts := api.Options{
		DBPath:             dbPath,
		Port:               port,
		BindHost:           bindHost,
		BaseURL:            os.Getenv("BASE_URL"),
		BrandingFS:         brandingFS,
		AdminEmail:         os.Getenv("ADMIN_EMAIL"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		ContactEmail:       os.Getenv("CONTACT_EMAIL"),
		CookieSecret:       cookieSecret,
		Prod:               prod,
		MasterKey:          os.Getenv("ASSOKIT_MASTER_KEY"),
		OAuthSigningKey:    os.Getenv("OAUTH_SIGNING_KEY"),
		OAuthAllowInsecure: os.Getenv("OAUTH_ALLOW_INSECURE") == "true",
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		BrandingUploadDir:  os.Getenv("BRANDING_DIR"),
		TrustProxyHeaders:  os.Getenv("TRUST_PROXY_HEADERS") == "true",
		WebhookURL:         os.Getenv("WEBHOOK_URL"),
		WebhookSecret:      os.Getenv("WEBHOOK_SECRET"),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPUser:           os.Getenv("SMTP_USER"),
		SMTPPass:           os.Getenv("SMTP_PASS"),
		SMTPPort:           smtpPort,
		SMTPFrom:           os.Getenv("SMTP_FROM"),
		SMTPAdminTo:        os.Getenv("SMTP_ADMIN_TO"),
		ResendAPIKey:       os.Getenv("RESEND_API_KEY"),
		Logger:             logger,
	}

	app, err := api.New(opts)
	if err != nil {
		log.Fatalf("initialisation: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		slog.Info("démarrage serveur", "port", port)
		if err := app.ListenAndServe(ctx); err != nil {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		if err != nil {
			slog.Error("erreur serveur", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("signal d'arrêt reçu, fermeture en cours...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Shutdown(shutdownCtx); err != nil {
			slog.Error("erreur arrêt", "error", err)
		}
	}
}
