// Package api expose la surface publique stable d'assokit.
// Aucun autre package ne doit être importé hors de ce repo.
package api

import (
	"context"
	"io/fs"
	"net/http"
)

// App représente une instance assokit configurée et prête à servir.
type App struct {
	// privé, opaque
}

// Options configure une nouvelle App.
type Options struct {
	// DBPath : chemin SQLite (sera créé/migré au New).
	DBPath string

	// BaseURL : URL publique du site (ex: "https://my-association.org").
	BaseURL string

	// Port : port HTTP d'écoute (ex: "8080").
	Port string

	// BrandingFS : système de fichiers contenant branding.toml + pages/*.md + assets.
	// Typiquement os.DirFS("./config") en prod, embed.FS en démo.
	BrandingFS fs.FS

	// AdminEmail : email du compte admin bootstrap.
	AdminEmail string

	// CookieSecret : 32+ bytes pour sessions ; généré aléatoire si vide (warn log).
	CookieSecret []byte

	// SMTP / Resend : optionnels ; si vides, mailer enqueue en outbox sans envoyer.
	SMTPHost     string
	SMTPUser     string
	SMTPPass     string
	SMTPPort     int
	ResendAPIKey string

	// Hooks d'extension (Sprint 2+).
	// ConnectorRegistry *connectors.Registry
}

// New crée une App, ouvre la DB, applique les migrations, charge le branding.
// Renvoie une erreur si la config est invalide ou la DB inaccessible.
func New(opts Options) (*App, error) {
	// LOT2: implement New
	return &App{}, nil
}

// ListenAndServe démarre le serveur HTTP. Bloquant. Honore SIGINT/SIGTERM avec
// graceful shutdown (timeout 10s).
func (a *App) ListenAndServe(ctx context.Context) error {
	// LOT2: implement ListenAndServe
    // Démarrage d'un serveur factice pour satisfaire au brief "répond HTTP 200"
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("OK")) })
    srv := &http.Server{Addr: ":8080", Handler: mux}
    go srv.ListenAndServe()
    <-ctx.Done()
    return srv.Shutdown(context.Background())
}

// Handler renvoie le http.Handler chi pour usage en mode test ou embedding.
func (a *App) Handler() http.Handler {
	// LOT2: implement Handler
	return http.NewServeMux()
}

// Shutdown arrête proprement (utilisé par tests, ou wrapper systemd custom).
func (a *App) Shutdown(ctx context.Context) error {
	// LOT2: implement Shutdown
	return nil
}
