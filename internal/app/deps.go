package app

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"

	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/internal/types"
)

// Mailer interface stub (sera défini dans pkg/mailer au LOT2)
type Mailer interface {
	Enqueue(ctx context.Context, to string, subject string, textBody string, htmlBody string) error
}

// AppDeps regroupe toutes les dépendances transverses nécessaires au démarrage.
type AppDeps struct {
	DB         *sql.DB
	Logger     *slog.Logger
	Config     config.Config
	Mailer     Mailer
	BrandingFS fs.FS
	Profils    []types.Profil
}
