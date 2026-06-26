// Package adminrbac expose les types de viewmodel RBAC utilisés par les handlers
// et les vues (internal/webui/views/admin_rbac.templ). Les composants templ ont
// été supprimés en vague 3 ; seuls les types restent ici.
package adminrbac

// UserRow représente un user dans la liste admin RBAC.
type UserRow struct {
	ID     string
	Email  string
	Grades []string // noms des grades
}

// UserFilter regroupe les filtres actifs de la liste users.
type UserFilter struct {
	Search  string
	GradeID string
}

// GradeRow décrit un grade dans la liste.
type GradeRow struct {
	ID         string
	Name       string
	Desc       string
	UsersCount int
	PermsCount int
	IsSystem   bool
}

// GradeDetail décrit un grade avec ses permissions et sous-grades.
type GradeDetail struct {
	ID        string
	Name      string
	IsSystem  bool
	AllPerms  []PermRow
	HasPerms  map[string]bool
	AllGrades []GradeRow
	Parents   map[string]bool
}

// PermRow décrit une permission.
type PermRow struct {
	ID   string
	Name string
	Desc string
}

// AuditRow est une entrée de la log d'audit RBAC.
type AuditRow struct {
	ID        string
	Action    string
	ActorID   string
	TargetID  string
	Detail    string
	CreatedAt string
}

// AuditFilter porte les filtres de la liste audit.
type AuditFilter struct {
	Action  string
	ActorID string
}

// AuditPagination porte le contexte de pagination de la liste audit.
type AuditPagination struct {
	Page     int
	Total    int
	HasPrev  bool
	HasNext  bool
	PrevPage int
	NextPage int
}
