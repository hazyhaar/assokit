// Package signupprofile porte le catalogue des profils d'inscription d'une
// communauté à membres. Valeurs pures, sans dépendance : le catalogue est
// injecté au core à la bordure (api.Options), jamais choisi par le core.
package signupprofile

// DefaultGrade est le grade RBAC assigné à un profil qui n'en précise pas.
const DefaultGrade = "sys-member"

// Profile décrit un profil d'inscription proposé sur /participer.
type Profile struct {
	ID       string       `json:"id"`
	Icon     string       `json:"icon,omitempty"`
	Label    string       `json:"label"`
	Subtitle string       `json:"subtitle,omitempty"`
	GradeID  string       `json:"grade_id,omitempty"`
	Extra    []ExtraField `json:"extra,omitempty"`
}

// ExtraField décrit un champ supplémentaire conditionnel à un profil.
type ExtraField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// Grade retourne le grade RBAC du profil : GradeID s'il est précisé, sinon
// DefaultGrade.
func (p Profile) Grade() string {
	if p.GradeID != "" {
		return p.GradeID
	}
	return DefaultGrade
}

// Find retourne le profil d'id donné dans le catalogue et un booléen de présence.
func Find(catalog []Profile, id string) (Profile, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}
