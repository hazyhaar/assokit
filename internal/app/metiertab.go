package app

// MetierTab décrit un onglet d'espace membre rattaché à un grade métier d'instance.
// Liste blanche injectée à la bordure : le core ne connaît que ce catalogue, jamais
// de nom de grade en dur. Les grades système (admin, member) en sont absents par
// construction — l'instance ne les déclare pas.
type MetierTab struct {
	Grade string
	Label string
	Route string
	// Hidden : l'entrée garde sa Route (requireMetierGrade) et masque la carte
	// d'accueil correspondante aux non-détenteurs, mais ne rend AUCUN onglet dans
	// la barre métier ni la page des profils. Sert à déclarer une garde par route
	// sans contaminer la navigation (ex. une sous-page métier atteinte par carte,
	// pas par onglet). Zéro valeur = onglet visible (comportement historique).
	Hidden bool
}

// AccountCard décrit une carte injectable dans un hub d'espace membre (ex.
// /account/elevage). Miroir du hook AdminDashboardGroups (pkg/api/api.go) mais
// pour l'espace membre : le core reste tenant-agnostic, il ne connaît aucune
// route propre à une instance verticale (ex. DEMOAPP) ; la bordure injecte ici les
// cartes qui relient ses sous-pages métier au hub socle. Rendu par le composant
// accountCard existant (internal/webui/views/account.templ) — jamais de markup
// écrit à la main dans la vue.
type AccountCard struct {
	Title string
	Desc  string
	Href  string
}
