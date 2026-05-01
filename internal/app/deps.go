package app

import (
	"database/sql"
	"context"
	"io/fs"
	"log/slog"

	"github.com/hazyhaar/assokit/internal/types"
	"github.com/hazyhaar/assokit/pkg/horui/theme"


	"github.com/hazyhaar/assokit/internal/config"
)

// Mailer interface stub (sera défini dans pkg/mailer au LOT2)
type Mailer interface {
	Enqueue(ctx context.Context, to string, subject string, textBody string, htmlBody string) error
}

// AppDeps regroupe toutes les dépendances transverses nécessaires au démarrage.
type AppDeps struct {
	Theme      *theme.Theme
	BrandingFS fs.FS
	Profils    []types.Profil
	DB     *sql.DB
	Logger *slog.Logger
	Config config.Config
	Mailer Mailer
}
