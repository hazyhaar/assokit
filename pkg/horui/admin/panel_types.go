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

// statusBadgeTone retourne le tone du badge templux selon le statut.
func statusBadgeTone(status string) string {
	switch status {
	case "pending":
		return "warning"
	case "triaged":
		return "info"
	case "closed":
		return "success"
	case "spam":
		return "danger"
	default:
		return "neutral"
	}
}

// intToStr convertit un int en string sans allocation (helper templ).
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// FeedbackRow est une ligne de la liste des feedbacks.
type FeedbackRow struct {
	ID          string
	PageURL     string
	MessageSnip string // 100 premiers chars
	Status      string
	CreatedAt   string
}

// FeedbackDetail contient les données complètes d'un feedback.
type FeedbackDetail struct {
	ID         string
	PageURL    string
	PageTitle  string
	Message    string
	IPHashSnip string // 4 derniers chars de ip_hash
	UserAgent  string
	Locale     string
	Status     string
	AdminNote  string
	TriagedBy  string
	TriagedAt  string
	CreatedAt  string
}

// FeedbackFilter regroupe les paramètres de filtrage actifs.
type FeedbackFilter struct {
	Status string
	Search string
}

// PaginationInfo porte le contexte de pagination.
type PaginationInfo struct {
	Page      int
	Total     int
	HasPrev   bool
	HasNext   bool
	PrevPage  int
	NextPage  int
	BaseQuery string // query string sans page=
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
