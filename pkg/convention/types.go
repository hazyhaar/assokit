// CLAUDE:SUMMARY Types du module convention de pâturage AFP : Convention, Clause,
// ConventionContext, l'interface ClauseProvider (côté consommateur) et son impl
// par défaut inerte. Import-clean (publiable, aucune dép horos55).
package convention

import (
	"context"
	"time"
)

// StatutsConvention énumère les valeurs autorisées du statut de cycle de vie
// d'une convention. Reflète la contrainte CHECK de la table conventions. Toute
// valeur hors de cette liste est rejetée à la création.
var StatutsConvention = []string{"brouillon", "active", "revoquee"}

// DureeMoisDefaut est la durée par défaut d'une convention de pâturage, en mois,
// quand aucune durée n'est fournie. Valeur conventionnelle (5 ans).
const DureeMoisDefaut = 60

// Convention est l'accord de mise à disposition de parcelles à un membre preneur,
// pour une durée et selon un statut de cycle de vie. Le contenu textuel des
// clauses n'est pas stocké : il est assemblé à la génération par un
// ClauseProvider injecté à la bordure.
type Convention struct {
	ID        string
	PreneurID string
	Parcelles []string // identifiants de parcelles rattachées
	DureeMois int
	Statut    string
	CreatedAt time.Time
}

// Clause est un article de la convention, produit par le fournisseur de clauses.
// Ref est un identifiant stable d'article (ex. "duree", "usage_pastoral") ;
// Source documente l'origine de la clause (corpus, version) pour traçabilité.
type Clause struct {
	Ref    string `json:"ref"`
	Titre  string `json:"titre"`
	Texte  string `json:"texte"`
	Source string `json:"source"`
}

// ConventionContext décrit la situation pour laquelle des clauses sont demandées.
// C'est l'entrée du fournisseur de clauses : le module assemble ce contexte à
// partir de la convention en cours de génération, sans connaître la provenance
// réelle des clauses. Type assokit : aucune dépendance externe ne fuite ici.
type ConventionContext struct {
	PreneurID      string   `json:"preneur_id"`
	Parcelles      []string `json:"parcelles"`
	DureeMois      int      `json:"duree_mois"`
	UsagePrincipal string   `json:"usage_principal"` // ex. "pastoral"
}

// ClauseProvider fournit le contenu des clauses d'une convention. L'interface est
// définie CÔTÉ CONSOMMATEUR (assokit) : le core ne connaît ni n'importe le
// producteur réel des clauses. La bordure d'une instance plateforme injecte un
// adaptateur qui désérialise un contrat JSON neutre vers []Clause ; en l'absence
// d'un tel adaptateur, l'implémentation par défaut inerte est utilisée.
type ClauseProvider interface {
	Clauses(ctx context.Context, c ConventionContext) ([]Clause, error)
}

// InertProvider est l'implémentation par défaut du fournisseur de clauses. Elle
// est inerte : elle ne fabrique AUCUNE clause (état vide assumé, jamais de fausse
// clause). Une instance qui ne branche pas de fournisseur réel génère donc une
// convention sans articles — état vide explicite, conforme à la doctrine « jamais
// de mock ». La bordure d'une instance plateforme remplace InertProvider par un
// adaptateur qui appelle le service de clauses réel.
type InertProvider struct{}

// Clauses retourne systématiquement une liste vide, sans erreur. Aucune clause
// n'est inventée.
func (InertProvider) Clauses(_ context.Context, _ ConventionContext) ([]Clause, error) {
	return []Clause{}, nil
}
