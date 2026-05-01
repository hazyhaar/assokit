package app

import (
	"database/sql"
	"log/slog"

	"github.com/hazyhaar/assokit/internal/config"
)

// Mailer interface stub (sera défini dans pkg/mailer au LOT2)
type Mailer interface {
	// LOT2: définir les méthodes de l'interface Mailer
}

// AppDeps regroupe toutes les dépendances transverses nécessaires au démarrage.
type AppDeps struct {
	DB     *sql.DB
	Logger *slog.Logger
	Config config.Config
	Mailer Mailer
}
