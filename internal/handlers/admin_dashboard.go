// CLAUDE:SUMMARY handleAdminDashboard — tableau de bord /admin listant toutes les
// fonctions de gestion + les espaces membre (pour tester le point de vue de chaque
// profil). Gating requireAdmin au niveau route.
package handlers

import (
	"net/http"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
)

// handleAdminDashboard rend le tableau de bord de gestion. Les groupes de liens
// sont déclarés ici ; pour un kit multi-instance, un registre extensible par
// module remplacerait cette liste codée en dur.
func handleAdminDashboard(deps app.AppDeps) http.HandlerFunc {
	groups := []views.DashGroup{
		{Title: "Foncier", Links: []views.DashLink{
			{Label: "Registre parcellaire", Href: "/admin/parcelles", Desc: "Parcelles, propriétaires, droits réels"},
			{Label: "Conventions de pâturage", Href: "/admin/conventions", Desc: "Rédaction, génération, révocation"},
		}},
		{Title: "Vie associative", Links: []views.DashLink{
			{Label: "Adhésions & cotisations", Href: "/admin/memberships", Desc: "Membres et statuts"},
			{Label: "Newsletters", Href: "/admin/newsletters", Desc: "Diffusion aux membres"},
		}},
		{Title: "Relations", Links: []views.DashLink{
			{Label: "Dons", Href: "/admin/donations", Desc: "Suivi et export"},
			{Label: "Retours des membres", Href: "/admin/feedbacks", Desc: "Feedbacks reçus"},
		}},
		{Title: "Configuration", Links: []views.DashLink{
			{Label: "Diagnostic", Href: "/admin/setup", Desc: "État de la configuration"},
		}},
		{Title: "Espaces membre (tester chaque profil)", Links: []views.DashLink{
			{Label: "Mes parcelles", Href: "/account/parcelles", Desc: "Point de vue d'un propriétaire"},
			{Label: "Mes conventions", Href: "/account/conventions", Desc: "Point de vue d'un éleveur preneur"},
			{Label: "Mon adhésion", Href: "/account/membership", Desc: "Point de vue d'un membre"},
			{Label: "Mes données personnelles", Href: "/account/data-download", Desc: "Export RGPD du membre"},
			{Label: "Messagerie", Href: "/messages", Desc: "Échanges entre membres"},
		}},
		{Title: "Pages publiques", Links: []views.DashLink{
			{Label: "Accueil", Href: "/"},
			{Label: "FAQ", Href: "/faq"},
			{Label: "Lexique", Href: "/lexique"},
			{Label: "Politique de données", Href: "/donnees-personnelles"},
			{Label: "Forum", Href: "/forum"},
			{Label: "Événements", Href: "/events"},
		}},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		renderPageV2(w, r, deps, "Gestion", views.AdminDashboard(groups))
	}
}
