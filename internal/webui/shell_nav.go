package webui

import (
	"slices"

	"github.com/hazyhaar/assokit/internal/webui/templux"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/identity"
)

func userDropdownItems(u *identity.User) []templux.DropdownItem {
	items := []templux.DropdownItem{
		{Label: "Mon compte", Href: "/account"},
		{Label: "Agenda", Href: "/events"},
	}
	if slices.Contains(u.Roles, "admin") {
		items = append(items, templux.DropdownItem{Label: "Gestion", Href: "/admin"})
	}
	items = append(items, templux.DropdownItem{
		Label: theme.T("button.logout", "Déconnexion"),
		Href:  "/logout",
	})
	return items
}
