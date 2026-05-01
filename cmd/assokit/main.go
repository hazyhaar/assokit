package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hazyhaar/assokit/pkg/api"
)

func main() {
	// Initialisation basique du logger pour le démarrage
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Lecture minimale des variables d'environnement nécessaires au constructeur (stub)
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

	opts := api.Options{
		DBPath:     dbPath,
		Port:       port,
		BrandingFS: os.DirFS(brandingDir),
	}

	// Initialisation de l'application
	app, err := api.New(opts)
	if err != nil {
		log.Fatalf("Erreur fatale lors de l'initialisation: %v", err)
	}

	// Gestion de l'arrêt gracieux
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		slog.Info("Démarrage du serveur", "port", port)
		// Ce stub dans api.go va faire un panic() dans le lot 1
		// On l'emballe dans un recover pour le test go run du lot 1
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic caught: %v", r)
			}
		}()
		if err := app.ListenAndServe(ctx); err != nil {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		if err != nil {
			slog.Error("Erreur serveur", "error", err)
		}
	case <-ctx.Done():
		slog.Info("Signal d'arrêt reçu, fermeture en cours...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Shutdown(shutdownCtx); err != nil {
			slog.Error("Erreur lors de l'arrêt", "error", err)
		}
	}
}
