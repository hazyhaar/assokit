// CLAUDE:SUMMARY Couche RGPD transverse : types du journal d'accès nominatif + finalités et durées de conservation (const). Import-clean (publiable).
package gdpr

import "time"

// Natures de sujet du journal d'accès. Le sujet d'un accès nominatif n'est pas
// nécessairement un utilisateur : une parcelle ou un droit cadastral porte aussi
// des données nominatives (propriétaire, occupant).
const (
	SubjectUser     = "user"
	SubjectParcelle = "parcelle"
	SubjectDroit    = "droit"
	SubjectAssembly = "assembly"
)

// Actions journalisées. Une consultation lit des données nominatives ; un export
// les restitue à la personne concernée (portabilité RGPD).
const (
	ActionView   = "view"
	ActionExport = "export"
)

// AccessLog est une entrée du journal des accès nominatifs. Append-only : une
// fois écrite, elle n'est ni modifiée ni supprimée par le code applicatif.
type AccessLog struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`      // sujet rattaché à un compte, si connu (peut être vide)
	SubjectKind string    `json:"subject_kind"` // SubjectUser | SubjectParcelle | SubjectDroit
	SubjectID   string    `json:"subject_id"`   // identifiant de la donnée consultée
	ActorID     string    `json:"actor_id"`     // identité ayant effectué l'accès
	Action      string    `json:"action"`       // ActionView | ActionExport
	At          time.Time `json:"at"`
}

// RetentionCategory documente, pour une catégorie de données nominatives, la
// finalité du traitement et la durée de conservation. Rendu sur la page de
// politique de données ; aucune purge automatique en v1 (différé it.2).
type RetentionCategory struct {
	Key       string // identifiant stable
	Label     string // libellé user-facing
	Purpose   string // finalité du traitement
	Retention string // durée de conservation exprimée en clair
}

// RetentionCatalog liste les catégories de données traitées par une instance
// assokit et leur régime de conservation. Valeurs réelles, pas de placeholder :
// ce catalogue est la source unique rendue sur la page de politique de données.
//
// Conservation différée (it.2) : aucune purge automatique n'est appliquée à ce
// stade ; les durées ci-dessous documentent l'engagement et serviront de base à
// l'anonymisation programmée d'une itération ultérieure.
var RetentionCatalog = []RetentionCategory{
	{
		Key:       "compte",
		Label:     "Compte membre",
		Purpose:   "Gérer l'adhésion et l'accès au site de la communauté.",
		Retention: "Conservé tant que le compte est actif ; supprimé sur demande d'effacement.",
	},
	{
		Key:       "cadastre",
		Label:     "Registre parcellaire",
		Purpose:   "Tenir le registre des parcelles et des droits des membres de l'association foncière.",
		Retention: "Conservé pour la durée de l'appartenance à l'association foncière.",
	},
	{
		Key:       "messages",
		Label:     "Messagerie entre membres",
		Purpose:   "Permettre les échanges privés entre membres.",
		Retention: "Conservés tant que le compte existe ; effacés à la suppression du compte.",
	},
	{
		Key:       "journal_acces",
		Label:     "Journal des accès nominatifs",
		Purpose:   "Tracer qui a consulté ou exporté des données personnelles, à des fins de redevabilité.",
		Retention: "Conservé à des fins de preuve d'accès ; purge programmée différée.",
	},
}
