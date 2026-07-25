package app

// GrantableGrade décrit un grade métier qu'un membre peut solliciter et que la
// gouvernance peut octroyer. Porte l'identifiant RBAC (AssignGrade) et le nom
// (projection u.Roles, garde requireMetierGrade).
type GrantableGrade struct {
	ID   string
	Name string
}

// ProfileGrant configure l'octroi de profils métier pour une instance : grade de
// gouvernance (octroi/retrait) et catalogue des grades requestables. Injecté à la
// bordure ; le core ne connaît aucun nom de grade en dur.
type ProfileGrant struct {
	GovernanceGradeID   string
	GovernanceGradeName string
	Requestable         []GrantableGrade
}
