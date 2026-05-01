package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hazyhaar/assokit/pkg/api"
)

//go:embed config/*
var configFS embed.FS

func main() {
	opts := api.Options{
		DBPath:     "asso.db",
		Port:       "8080",
		BaseURL:    "http://localhost:8080",
		BrandingFS: configFS,
	}

	app, err := api.New(opts)
	if err != nil {
		log.Fatalf("Échec de l'initialisation: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic caught: %v", r)
			}
		}()
		if err := app.ListenAndServe(ctx); err != nil {
			errChan <- err
		}
	}()

	fmt.Printf("Instance minimale assokit démarrée sur le port %s\n", opts.Port)

	select {
	case <-ctx.Done():
		fmt.Println("Arrêt en cours...")
		app.Shutdown(context.Background())
	case err := <-errChan:
		log.Fatalf("Erreur serveur: %v", err)
	}
}
