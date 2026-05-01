package types

// Profil représente un profil d'inscription (ex: lanceur, asso).
type Profil struct {
	Key         string   `toml:"key"`
	Label       string   `toml:"label"`
	ExtraFields []string `toml:"extra_fields"`
}
