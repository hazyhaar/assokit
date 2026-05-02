package admin

// PanelField décrit un champ du panel d'administration branding.
type PanelField struct {
	Key         string
	Section     string
	Order       int
	Label       string
	Hint        string
	Kind        string // text,longtext,url,iban,bic,file,color,int
	Placeholder string
	Required    bool
	MaxBytes    int
	MimeAllow   []string
}

// SectionLabel retourne le libellé humain d'une section.
func SectionLabel(section string) string {
	switch section {
	case "identite":
		return "Identité de l'association"
	case "presentation":
		return "Présentation et visuels"
	case "helloasso":
		return "HelloAsso — dons et adhésions"
	case "virement":
		return "Virement bancaire"
	default:
		return section
	}
}

// SectionOrder retourne l'ordre de rendu d'une section.
func SectionOrder(section string) int {
	switch section {
	case "identite":
		return 1
	case "presentation":
		return 2
	case "helloasso":
		return 3
	case "virement":
		return 4
	default:
		return 99
	}
}
