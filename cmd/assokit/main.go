package main

import (
	"context"
	"encoding/hex"
	"log"
	"log/slog"
	"os"
	"os/signal"
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

	var cookieSecret []byte
	if s := os.Getenv("COOKIE_SECRET"); s != "" {
		b, err := hex.DecodeString(s)
		if err != nil {
			log.Fatalf("COOKIE_SECRET: hex decode: %v", err)
		}
		cookieSecret = b
	}

	smtpPort, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))

	opts := api.Options{
		DBPath:        dbPath,
		Port:          port,
		BaseURL:       os.Getenv("BASE_URL"),
		BrandingFS:    os.DirFS(brandingDir),
		AdminEmail:    os.Getenv("ADMIN_EMAIL"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		ContactEmail:  os.Getenv("CONTACT_EMAIL"),
		CookieSecret:  cookieSecret,
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPUser:      os.Getenv("SMTP_USER"),
		SMTPPass:      os.Getenv("SMTP_PASS"),
		SMTPPort:      smtpPort,
		SMTPFrom:      os.Getenv("SMTP_FROM"),
		SMTPAdminTo:   os.Getenv("SMTP_ADMIN_TO"),
		ResendAPIKey:  os.Getenv("RESEND_API_KEY"),
		Logger:        logger,
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
