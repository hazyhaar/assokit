// CLAUDE:SUMMARY Types du module gouvernance associative (socle réunion V2a) : Assembly
// (assemblée générale), Convocation (trace d'envoi par adhérent) et l'énumération des
// statuts de cycle de vie. Import-clean (publiable, aucune dépendance interne).
package governance

import "time"

// StatutsAssembly énumère les valeurs autorisées du statut de cycle de vie d'une
// assemblée générale. Reflète la contrainte CHECK de la table assemblies. Toute
// valeur hors de cette liste est rejetée à la création. Statut initial : drafting.
var StatutsAssembly = []string{"drafting", "convoked", "cancelled"}

// Assembly est une assemblée générale de la communauté : un ordre du jour et une
// date programmée, qui traverse un cycle de vie réduit (brouillon, convoquée,
// annulée). Aucun quorum, vote ni procès-verbal à ce stade (lots ultérieurs).
type Assembly struct {
	ID          string
	Name        string
	Agenda      string // ordre du jour, texte libre
	Status      string
	ScheduledAt string // date programmée, texte (ex. "2026-09-12 18:00")
	CreatedAt   time.Time
}

// Convocation trace l'envoi d'un courriel de convocation à un adhérent pour une
// assemblée donnée. Une ligne par adhérent effectivement contacté, horodatée.
type Convocation struct {
	ID         string
	AssemblyID string
	UserID     string
	SentAt     time.Time
}

// Mandate est un pouvoir de représentation (V2b) : un mandant (principal) confie à
// un mandataire (proxy) le soin de le représenter à une assemblée. Au plus un mandat
// par mandant et par assemblée (contrainte d'unicité). Le plafond du nombre de
// mandats portés par un même mandataire est différé (it.2) — non contraint ici.
type Mandate struct {
	ID          string
	AssemblyID  string
	PrincipalID string // mandant représenté
	ProxyID     string // mandataire qui représente
	CreatedAt   time.Time
}

// Attendance est un émargement (V2b) : trace qu'un adhérent est présent à une
// assemblée. Un émargement unique par membre et par assemblée (idempotent). La
// méthode documente le canal de signature (ex. "manual" : pointage en séance).
type Attendance struct {
	ID           string
	AssemblyID   string
	MemberUserID string
	Method       string
	SignedAt     time.Time
}

// StatutsResolution énumère les valeurs autorisées du statut d'une résolution.
// Reflète la contrainte CHECK de la table resolutions. Cycle : open puis closed.
var StatutsResolution = []string{"open", "closed"}

// ChoixVote énumère les choix de bulletin autorisés. Reflète la contrainte CHECK
// de la table votes.
var ChoixVote = []string{"pour", "contre", "abstention"}

// Resolution est une question soumise au vote d'une assemblée (V2c) : un libellé et
// un statut de scrutin (ouvert puis clos). Les bulletins ne sont acceptés que tant
// que le scrutin est ouvert. Le vote pondéré et la procuration sont différés (it.2).
type Resolution struct {
	ID         string
	AssemblyID string
	Label      string // la question soumise au vote, texte libre
	Status     string // open | closed
	CreatedAt  time.Time
}

// Vote est un bulletin individuel (V2c) : un votant exprime un choix sur une
// résolution. Au plus un vote par couple (résolution, votant) — l'unicité en base
// interdit le double vote. Le vote est un acte enregistré, jamais écrasé.
type Vote struct {
	ID           string
	ResolutionID string
	VoterUserID  string
	Choice       string // pour | contre | abstention
	CastAt       time.Time
}

// Deliberation est une entrée du registre des délibérations (V2d) : la consignation
// FIGÉE du dépouillement d'une résolution close. Les comptes (Pour/Contre/Abstention/
// Total) sont copiés en colonnes au moment de la consignation, jamais recalculés —
// c'est la trace immuable de la décision. Au plus une délibération par résolution
// (UNIQUE en base) ; une seconde consignation est rejetée, jamais écrasée.
type Deliberation struct {
	ID           string
	AssemblyID   string
	ResolutionID string
	Label        string // libellé de la résolution, copié au moment de la consignation
	Pour         int
	Contre       int
	Abstention   int
	Total        int
	RecordedAt   time.Time
}

// Minutes est le procès-verbal d'une assemblée (V2d) : un instantané texte du
// déroulé (nom, ordre du jour, délibérations consignées avec leurs dépouillements).
// Au plus un PV par assemblée (UNIQUE en base) ; immuable une fois généré, une
// régénération est rejetée. L'approbation et la diffusion sont différées (it.2).
type Minutes struct {
	ID          string
	AssemblyID  string
	Body        string // instantané texte du PV
	GeneratedAt time.Time
}

// Tally est le dépouillement d'une résolution (V2c) : décompte déterministe des
// bulletins par choix. Total est le nombre de votes effectivement enregistrés (la
// population votante réelle), PAS la population éligible.
type Tally struct {
	Pour       int
	Contre     int
	Abstention int
	Total      int
}

// QuorumResult est le décompte de quorum (V2b) calculé pour une assemblée et une
// population éligible donnée. La population éligible (Eligible) est CALCULÉE PAR
// L'APPELANT (handler/action, via membership.Store) et passée à ComputeQuorum : le
// module gouvernance reste agnostique de la règle « qui est adhérent actif ».
type QuorumResult struct {
	Eligible    int     // taille de la population éligible (len(eligibleIDs))
	Present     int     // émargés dont le membre est éligible
	Represented int     // mandats valides comptés (mandant éligible non présent, mandataire éligible présent)
	Attending   int     // present + represented, sans double comptage d'un même adhérent
	Threshold   float64 // seuil appliqué (paramètre, ex. 0.5)
	Reached     bool    // Attending >= ceil(Eligible*Threshold)
}
