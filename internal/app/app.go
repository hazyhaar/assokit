package app

import (
	"context"
	"net/http"

	"github.com/hazyhaar/assokit/pkg/api"
)

// App représente l'instance interne d'assokit.
type App struct {
	opts api.Options
	deps AppDeps
}

// New crée une nouvelle instance de l'application interne.
func New(opts api.Options) (*App, error) {
	// LOT2: implement New
	return &App{opts: opts}, nil
}

func (a *App) ListenAndServe(ctx context.Context) error {
	// LOT2: implement ListenAndServe
	return nil
}

func (a *App) Handler() http.Handler {
	// LOT2: implement Handler
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	// LOT2: implement Shutdown
	return nil
}
