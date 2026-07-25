// CLAUDE:SUMMARY Module corpus d'assokit : interface Provider (côté consommateur)
// pour interroger un corpus documentaire externe, et son implémentation par défaut
// inerte. Import-clean (publiable, aucune dépendance horos55) : assokit ne connaît
// que l'interface, jamais le moteur réel (siftrag, etc.) qu'une bordure injecte.
package corpus

import "context"

// Hit est un résultat de recherche dans le corpus. Il est volontairement plat et
// neutre : aucune notion de moteur sous-jacent ne fuite dans le core assokit.
//
//   - Content : le texte de l'extrait retourné (déjà produit par le moteur ; jamais
//     fabriqué côté assokit).
//   - Score : le score de pertinence du moteur (échelle propre au moteur, comparable
//     entre hits d'une même requête).
//   - Ref : l'ancrage de provenance — l'identifiant stable de l'extrait (chunk id).
//     Affiché pour la traçabilité : chaque résultat est rattaché à sa pièce d'origine.
//   - Source : citation ou URL d'ancrage légal/officiel si le moteur la fournit ;
//     chaîne vide assumée sinon (aucune source n'est inventée).
type Hit struct {
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Ref     string  `json:"ref"`
	Source  string  `json:"source"`
}

// Provider interroge un corpus documentaire et retourne des extraits sourcés.
// L'interface est définie CÔTÉ CONSOMMATEUR (assokit) : le core ne connaît ni
// n'importe le moteur réel. La bordure d'une instance plateforme injecte un
// adaptateur qui appelle le moteur (ex. siftrag, corpus de droit afp_droit) ; en
// l'absence d'un tel adaptateur, l'implémentation par défaut inerte est utilisée.
type Provider interface {
	SearchCorpus(ctx context.Context, query string, limit int) ([]Hit, error)
}

// InertProvider est l'implémentation par défaut. Elle est inerte : elle ne
// fabrique AUCUN résultat (état vide assumé, jamais d'extrait inventé — règle dure
// en matière juridique). Une instance qui ne branche pas de moteur réel retourne
// donc systématiquement zéro résultat. La bordure d'une instance plateforme
// remplace InertProvider par un adaptateur qui interroge le corpus réel.
type InertProvider struct{}

// SearchCorpus retourne systématiquement une liste vide, sans erreur. Aucun
// résultat n'est inventé.
func (InertProvider) SearchCorpus(_ context.Context, _ string, _ int) ([]Hit, error) {
	return []Hit{}, nil
}

// IsInert indique si le fournisseur est absent ou inerte (aucun moteur branché).
func IsInert(p Provider) bool {
	if p == nil {
		return true
	}
	_, ok := p.(InertProvider)
	return ok
}
