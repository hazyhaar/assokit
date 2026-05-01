package config

import (
	"io/fs"
)

// Branding représente la configuration de marque de l'association.
type Branding struct {
	Name    string `toml:"name"`
	Contact string `toml:"contact"`
	BaseURL string `toml:"base_url"`
	Colors  struct {
		Primary string `toml:"primary"`
	} `toml:"colors"`
	Footer string `toml:"footer"`
}

// LoadBrandingTOML lit et parse le fichier branding.toml depuis le fs.FS fourni.
func LoadBrandingTOML(brandingFS fs.FS) (Branding, error) {
	// LOT3: implémentation via github.com/pelletier/go-toml/v2
	return Branding{}, nil
}
