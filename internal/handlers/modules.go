// CLAUDE:SUMMARY Registre des modules d'espace membre désactivables par la bordure
// (api.Options.DisabledModules). Un module = un slug + les surfaces d'espace membre
// (routes + hrefs) qu'il possède. Le core reste tenant-agnostic : il ne connaît
// aucune instance, seule la bordure décide quels modules socle désactiver (ex. DEMOAPP
// remplace le module « conventions » par sa propre vue). Défaut : tous actifs.
package handlers

// moduleDef décrit un module d'espace membre désactivable. Hrefs recense les
// surfaces d'espace membre possédées par le module : routes montées
// conditionnellement (404 naturel si désactivé) et liens/cartes filtrés de l'UI.
type moduleDef struct {
	Slug  string
	Hrefs []string
}

// coreModules retourne le catalogue des modules d'espace membre du socle, communs à
// toute instance du kit. Chaque module ne recense ici que ses surfaces d'espace
// membre : une instance qui désactive le module fournit sa propre vue à la même
// place (routes non montées → 404, cartes/liens non rendus). La gestion admin
// éventuelle (ex. /admin/conventions) reste hors module — le core ne présume pas
// qu'une instance qui remplace la vue membre renonce aussi à l'outil de gestion.
func coreModules() []moduleDef {
	return []moduleDef{
		{Slug: "conventions", Hrefs: []string{"/account/conventions"}},
		{Slug: "parcelles", Hrefs: []string{"/account/parcelles"}},
	}
}

// KnownModuleSlugs retourne l'ensemble des slugs de module reconnus. Utilisé par la
// bordure (api.New) pour valider Options.DisabledModules en fail-loud : un slug
// inconnu est une erreur de configuration, jamais un silence.
func KnownModuleSlugs() map[string]bool {
	out := make(map[string]bool)
	for _, m := range coreModules() {
		out[m.Slug] = true
	}
	return out
}

// ValidateDisabledModules vérifie que chaque slug fourni par la bordure désigne un
// module reconnu. Retourne une erreur nommant le premier slug inconnu (fail-loud),
// pour qu'une faute de frappe dans la configuration d'instance ne désactive jamais
// rien en silence.
func ValidateDisabledModules(slugs []string) error {
	known := KnownModuleSlugs()
	for _, s := range slugs {
		if !known[s] {
			return &UnknownModuleError{Slug: s}
		}
	}
	return nil
}

// UnknownModuleError signale un slug de module absent du catalogue socle.
type UnknownModuleError struct{ Slug string }

func (e *UnknownModuleError) Error() string {
	return "module inconnu " + e.Slug + " (aucun module socle ne porte ce slug)"
}

// moduleForHref retourne le slug du module qui possède le href donné, ou "" si aucun.
// Égalité exacte : les surfaces sont déclarées explicitement, pas par préfixe, pour
// éviter qu'un module capture par inadvertance une route voisine.
func moduleForHref(href string) string {
	for _, m := range coreModules() {
		for _, h := range m.Hrefs {
			if h == href {
				return m.Slug
			}
		}
	}
	return ""
}

// moduleDisabled indique si le module possédant href est désactivé pour l'instance.
// href sans module rattaché → toujours actif.
func moduleDisabled(disabled map[string]bool, href string) bool {
	if len(disabled) == 0 {
		return false
	}
	slug := moduleForHref(href)
	return slug != "" && disabled[slug]
}
