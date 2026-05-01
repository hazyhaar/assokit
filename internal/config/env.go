package config

import (
	"fmt"
	"os"
)

// Config regroupe la configuration technique (non-branding) de l'application.
type Config struct {
	Port    string
	DBPath  string
	BaseURL string
}

// LoadEnv charge la configuration depuis les variables d'environnement.
func LoadEnv() (Config, error) {
	cfg := Config{
		Port:    os.Getenv("PORT"),
		DBPath:  os.Getenv("DB_PATH"),
		BaseURL: os.Getenv("BASE_URL"),
	}

	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.DBPath == "" {
		return cfg, fmt.Errorf("DB_PATH est requis")
	}

	return cfg, nil
}
