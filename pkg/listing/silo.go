// Package listing fournit un store de listings marketplace tenant-agnostic.
// Chaque silo (vertical) est déclaré dans config/silos/<id>.toml ;
// les colonnes GENERATED SQLite et les index sont créés dynamiquement au boot.
// Aucune colonne métier n'est codée en dur.
package listing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// AttrDef décrit un attribut facettable d'un silo.
type AttrDef struct {
	Name     string `toml:"name"`
	JSONPath string `toml:"json_path"`
	Kind     string `toml:"kind"`    // "text" | "real"
	Indexed  bool   `toml:"indexed"` // true → CREATE INDEX
}

// RangeDef déclare un attribut supportant le filtrage min/max.
type RangeDef struct {
	Attr string `toml:"attr"`
}

// SiloDef est la définition complète d'un vertical chargée depuis config/silos/<id>.toml.
type SiloDef struct {
	ID          string     `toml:"id"`
	Label       string     `toml:"label"`
	Description string     `toml:"description"`
	Attrs       []AttrDef  `toml:"attrs"`
	Ranges      []RangeDef `toml:"ranges"`
}

// LoadSilo charge un fichier de config silo TOML depuis le chemin donné.
func LoadSilo(path string) (*SiloDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("listing.LoadSilo: %w", err)
	}
	var s SiloDef
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("listing.LoadSilo decode %s: %w", path, err)
	}
	if s.ID == "" {
		return nil, fmt.Errorf("listing.LoadSilo: silo id manquant dans %s", path)
	}
	return &s, nil
}

// LoadSilosDir charge tous les fichiers *.toml depuis un répertoire.
func LoadSilosDir(dir string) ([]*SiloDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing.LoadSilosDir: %w", err)
	}
	var silos []*SiloDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		s, err := LoadSilo(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		silos = append(silos, s)
	}
	return silos, nil
}
