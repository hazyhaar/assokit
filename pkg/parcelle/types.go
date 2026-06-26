// CLAUDE:SUMMARY Types du registre parcellaire AFP : Parcelle, Droit, ParcelleAvecDroits. Import-clean (publiable).
package parcelle

import "time"

// NaturesDuDroit énumère les valeurs autorisées de Droit.NatureDuDroit. Reflète
// la contrainte CHECK de la table parcelle_droits. La nature du droit est
// seulement stockée : aucune logique de vote ni de pondération des voix n'en
// dérive dans ce module.
var NaturesDuDroit = []string{"pleine_propriete", "usufruit", "nue_propriete", "occupant"}

// StatutsMad énumère les valeurs autorisées du statut de mise à disposition.
// Reflète la contrainte CHECK de la table parcelles. Toute valeur hors de cette
// liste est rejetée à la création.
var StatutsMad = []string{"libre", "mise_a_disposition"}

// Parcelle est une référence cadastrale rattachée à l'association foncière.
type Parcelle struct {
	ID             string
	CommuneCode    string
	Section        string
	NumeroParcelle string
	SurfaceM2      int64
	StatutMad      string
	CreatedAt      time.Time
}

// Droit relie une parcelle à un membre avec la nature de son droit.
type Droit struct {
	ID            string
	ParcelleID    string
	UserID        string
	NatureDuDroit string
	CreatedAt     time.Time
}

// ParcelleAvecDroits décore une parcelle de ses droits, pour l'affichage.
type ParcelleAvecDroits struct {
	Parcelle Parcelle
	Droits   []Droit
}
