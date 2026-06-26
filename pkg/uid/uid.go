// Package uid génère des identifiants UUIDv7 (triables temporellement).
//
// Wrapper mince au-dessus de google/uuid (déjà dépendance du kit). Volontairement
// import-clean : aucune dépendance interne horos55 (pas d'idgen) → ce bloc est
// publiable tel quel vers le repo open-source MIT.
package uid

import "github.com/google/uuid"

// New retourne un UUIDv7 sous forme de chaîne. UUIDv7 encode un timestamp en
// préfixe (RFC 9562) → les ids générés se trient naturellement par ordre de
// création (jobs, events, users, posts…), ce qui aide l'indexation SQLite.
// En cas d'échec improbable de la source d'entropie, repli sur UUIDv4.
func New() string {
	if v, err := uuid.NewV7(); err == nil {
		return v.String()
	}
	return uuid.New().String()
}
