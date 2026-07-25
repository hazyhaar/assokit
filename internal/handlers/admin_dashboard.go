// CLAUDE:SUMMARY handleAdminDashboard — tableau de bord /admin listant toutes les
// fonctions de gestion + les espaces membre (pour tester le point de vue de chaque
// profil). Gating requireAdmin au niveau route ; chaque lien peut porter une
// permission fine RBAC (DashLink.Perm) que le handler vérifie pour n'afficher que
// les liens réellement atteignables par l'utilisateur courant.
package handlers

import (
	"net/http"
	"slices"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// coreDashGroups retourne les groupes socle du tableau de bord, communs à toute
// instance du kit (aucune identité d'instance ici : les groupes propres à une
// instance sont injectés par la bordure et fusionnés séparément). Les liens gardés
// par une permission fine portent leur Perm ; les autres restent réservés aux
// administrateurs (le tableau de bord est déjà gardé par requireAdmin au niveau route).
func coreDashGroups() []views.DashGroup {
	return []views.DashGroup{
		{Title: "Foncier", Links: []views.DashLink{
			{Label: "Registre parcellaire", Href: "/admin/parcelles", Desc: "Parcelles, propriétaires, droits réels"},
			{Label: "Conventions de pâturage", Href: "/admin/conventions", Desc: "Rédaction, génération, révocation"},
		}},
		{Title: "Demandes et terrain", Links: []views.DashLink{
			{Label: "Demandes de mise à disposition", Href: "/admin/demandes", Desc: "Traiter les demandes des membres, les convertir en convention"},
			{Label: "Mutations déclarées", Href: "/admin/mutations", Desc: "Ventes et successions signalées par les propriétaires"},
			{Label: "Journal de terrain", Href: "/admin/field-reports", Desc: "Entretiens réalisés et problèmes signalés par les éleveurs"},
			{Label: "Intégration d'un propriétaire", Href: "/admin/onboard-proprietaire", Desc: "Parcours guidé d'arrivée (compte, adhésion, parcelles)"},
		}},
		{Title: "Gouvernance", Links: []views.DashLink{
			{Label: "Assemblées et délibérations", Href: "/admin/governance", Desc: "Assemblées générales, convocations, émargement, votes, procès-verbaux"},
		}},
		{Title: "Accès et permissions", Links: []views.DashLink{
			{Label: "Grades et permissions", Href: "/admin/rbac/grades", Desc: "Définir les grades et leurs droits", Perm: "rbac.grades.read"},
			{Label: "Comptes et rôles", Href: "/admin/rbac/users", Desc: "Attribuer des grades aux membres", Perm: "rbac.users.read"},
			{Label: "Journal des accès", Href: "/admin/rbac/audit", Desc: "Historique des changements de droits", Perm: "rbac.audit.read"},
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
			{Label: "Personnalisation du site", Href: "/admin/panel", Desc: "Nom, charte, textes et visuels de l'instance"},
			{Label: "Services externes", Href: "/admin/connectors", Desc: "Connexion aux outils tiers (paiement, visioconférence)"},
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
}

// handleAdminDashboard rend le tableau de bord de gestion. Les groupes socle
// (coreDashGroups) sont fusionnés avec les groupes injectés par la bordure
// (extraGroups, ex. la console du registre foncier d'une instance) : le core ne
// contient aucune identité d'instance, le hook générique la porte. Chaque lien
// gardé par une permission fine n'est affiché qu'à l'utilisateur qui la détient,
// pour que la vue reste cohérente avec le gate réel de la route (pas de lien
// visible qui mènerait à un 403, pas de lien caché sur une page autorisée).
func handleAdminDashboard(deps app.AppDeps, extraGroups []views.DashGroup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		u := middleware.UserFromContext(ctx)

		core := coreDashGroups()
		merged := make([]views.DashGroup, 0, len(core)+len(extraGroups))
		merged = append(merged, core...)
		merged = append(merged, extraGroups...)

		visible := make([]views.DashGroup, 0, len(merged))
		for _, g := range merged {
			links := make([]views.DashLink, 0, len(g.Links))
			for _, l := range g.Links {
				// Un lien vers la vue membre d'un module désactivé pour l'instance
				// n'est pas rendu : la route n'étant pas montée, il mènerait à un 404.
				if moduleDisabled(deps.DisabledModules, l.Href) {
					continue
				}
				if l.Perm != "" {
					if u == nil || deps.RBAC == nil {
						continue
					}
					if ok, _ := deps.RBAC.Can(ctx, u.ID, l.Perm); !ok {
						continue
					}
				}
				// Cohérence lien↔route : un lien dont la cible est gardée par un
				// grade métier (MetierTabs) n'est affiché qu'au détenteur du grade —
				// jamais un lien rendu qui mènerait à un 403 (ex. « Mes parcelles »
				// au cookie admin quand l'instance réserve la route aux propriétaires).
				if grade := metierGradeForRoute(deps.MetierTabs, l.Href); grade != "" {
					if u == nil || !slices.Contains(u.Roles, grade) {
						continue
					}
				}
				links = append(links, l)
			}
			if len(links) > 0 {
				visible = append(visible, views.DashGroup{Title: g.Title, Links: links})
			}
		}

		renderPageV2(w, r, deps, "Gestion", views.AdminDashboard(visible))
	}
}
