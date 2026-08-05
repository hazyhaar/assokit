// CLAUDE:SUMMARY MountRoutes — câble toutes les routes assokit sur le router chi.
package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	adminpanel "github.com/hazyhaar/assokit/internal/handlers/admin_panel"
	intoauth "github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/perms"
	svcrbac "github.com/hazyhaar/assokit/pkg/rbac"
	tree "github.com/hazyhaar/nodetree"
)

// routeConfig porte les options de bordure de MountRoutes.
type routeConfig struct {
	registerActions     func(*actions.Registry) error
	liveKitVault        *assets.Vault
	adminDashboardExtra []views.DashGroup
}

// RouteOption configure MountRoutes depuis la bordure (cmd/assokit ou un
// produit consommateur). Variadique → les appelants existants restent valides.
type RouteOption func(*routeConfig)

// WithExtraActions injecte un hook qui reçoit le registre d'actions après les
// seeds core, pour qu'un consommateur (ex. DEMOAPP) ajoute ses actions métier
// (route HTTP admin + outil MCP + permission RBAC automatiques). Le hook peut
// retourner une erreur (ex. ID dupliqué) → fatale au boot.
func WithExtraActions(fn func(*actions.Registry) error) RouteOption {
	return func(c *routeConfig) { c.registerActions = fn }
}

// WithLiveKitVault transmet le Vault LiveKit à MountRoutes pour activer
// l'action room.join (MCP) et la route GET /salon/{slug}/visio (HTTP).
func WithLiveKitVault(vault *assets.Vault) RouteOption {
	return func(c *routeConfig) { c.liveKitVault = vault }
}

// WithAdminDashboardGroups injecte des groupes de liens supplémentaires dans le
// tableau de bord /admin, fournis par la bordure (ex. la console du registre
// foncier d'une instance verticale). Le core reste tenant-agnostic : il ne
// contient aucune identité d'instance, seul ce hook générique la porte. Les
// groupes injectés sont fusionnés après les groupes socle et soumis au même
// filtrage par permission.
func WithAdminDashboardGroups(groups []views.DashGroup) RouteOption {
	return func(c *routeConfig) { c.adminDashboardExtra = groups }
}

// MountRoutes câble toutes les routes assokit sur r.
func MountRoutes(r chi.Router, deps app.AppDeps, opts ...RouteOption) error {
	var cfg routeConfig
	for _, o := range opts {
		o(&cfg)
	}
	feedbackRL := middleware.NewRateLimiter()
	// Sitemap initialisation
	sitemap := NewSitemap(deps.Config.BaseURL)
	sitemap.AddStatic(SitemapEntry{Loc: "/", Priority: 1.0, ChangeFreq: "weekly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/participer", Priority: 0.8, ChangeFreq: "monthly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/forum", Priority: 0.9, ChangeFreq: "daily"})
	sitemap.AddStatic(SitemapEntry{Loc: "/soutenir", Priority: 0.9, ChangeFreq: "monthly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/contact", Priority: 0.6, ChangeFreq: "monthly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/login", Priority: 0.4, ChangeFreq: "monthly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/register", Priority: 0.4, ChangeFreq: "monthly"})
	sitemap.AddStatic(SitemapEntry{Loc: "/search", Priority: 0.5, ChangeFreq: "monthly"})
	r.Get("/sitemap.xml", sitemap.Handler())
	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + deps.Config.BaseURL + "/sitemap.xml\n"))
	})

	treeStore := &tree.Store{DB: deps.DB}
	permsStore := &perms.Store{DB: deps.DB}

	// Pages vitrine
	r.Get("/", handlePage(deps, "home", treeStore))
	r.Get("/charte", handlePage(deps, "charte", treeStore))
	r.Get("/thematiques", handlePage(deps, "thematiques", treeStore))
	r.Get("/thematiques/{slug}", handleThematique(deps, treeStore))
	r.Get("/medias", handlePage(deps, "medias", treeStore))
	r.Get("/mentions-legales", handlePage(deps, "mentions-legales", treeStore))
	// CGU : page CMS servie par slug, contenu fourni par l'instance (BrandingFS
	// pages/cgu.md) ou gravé par l'admin. État vide assumé sinon.
	r.Get("/cgu", handlePage(deps, "cgu", treeStore))
	// FAQ et lexique : deux pages du CMS existant, servies par slug. Contenu
	// initial vide assumé (handlePage rend un état vide propre tant que l'admin
	// n'a pas créé la page via l'action pages.create / pages.update).
	r.Get("/faq", handlePage(deps, "faq", treeStore))
	r.Get("/lexique", handlePage(deps, "lexique", treeStore))
	r.Get("/merci", makeMerciHandler(deps))
	r.Get("/404", makeNotFoundHandler(deps))
	r.NotFound(makeNotFoundHandler(deps))

	// Soutenir / Contact
	r.Get("/soutenir", handleDonatePage(deps))
	// Aliases liens-externes/branding pour éviter 404 (prod branding.toml + layout.templ buttons).
	r.Get("/donate", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/soutenir", http.StatusMovedPermanently)
	})
	r.Get("/signup", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/participer", http.StatusMovedPermanently)
	})
	r.Get("/forgot-password", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/forgot", http.StatusMovedPermanently)
	})
	// /reset-password redirect géré plus bas avec préservation du query token (M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW).
	// Assets branding (logo.svg, og.png, favicon.ico, etc.) servis depuis BrandingFS.
	// Permet à branding.toml::logo_path = "assets/logo.svg" de résoudre /static/assets/logo.svg.
	if deps.BrandingFS != nil {
		if assetsFS, err := fs.Sub(deps.BrandingFS, "assets"); err == nil {
			r.Handle("/static/assets/*", http.StripPrefix("/static/assets/", http.FileServer(http.FS(assetsFS))))
		}
	}
	r.Get("/contact", handleContactPage(deps))
	r.Post("/contact", handleContactSubmit(deps))

	// Participer / Adhérer
	r.Get("/participer", handleParticiper(deps))
	r.Get("/adherer/{profil}", handleSignupForm(deps))
	r.Post("/adherer/{profil}", handleSignupSubmit(deps))

	// Activation magic link
	r.Get("/activate/{token}", handleActivate(deps))
	// Forum
	// Auto-bootstrap nœud racine 'forum' si absent (rule SEED-FORUM-ROOT-AUTO-BOOTSTRAP).
	if treeStore != nil {
		if _, err := ensureForumRoot(context.Background(), treeStore); err != nil {
			// Best-effort : log mais ne bloque pas le boot. Le handler /forum
			// re-tentera ensureForumRoot lui-même au premier accès.
			if deps.Logger != nil {
				deps.Logger.Warn("forum: ensureForumRoot au boot", "err", err)
			}
		}
	}
	r.Get("/forum", handleForumIndex(deps))
	// /forum/new AVANT /forum/{slug} pour matcher en priorité (chi route order).
	// GET du formulaire de création gardé par la MÊME garde d'écriture que son POST
	// (RequirePerm write sur node-forum) : cohérence vue<->handler — un visiteur qui
	// ne voit pas le bouton « Créer » (masqué par forumCanWrite) ne peut pas non plus
	// charger le formulaire (302 /login si non-connecté, 403 si connecté sans perm).
	r.Get("/forum/new",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumNewTopicForm(deps)).ServeHTTP)
	// Création d'une catégorie (enfant de la racine forum) — gardée comme les
	// autres mutations forum (colmate l'ancien /forum/new non gardé).
	r.Post("/forum/new",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumCreateTopic(deps)).ServeHTTP)
	// Station multi-panneaux : fragments HTMX (questions d'une catégorie, détail d'une question).
	r.Get("/forum/{slug}/questions", handleForumQuestions(deps))
	// GET formulaire de création de question — même garde d'écriture que le POST /question.
	r.Get("/forum/{slug}/new-question",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumNewQuestionForm(deps)).ServeHTTP)
	r.Get("/forum/{slug}/branches", handleForumBranches(deps))
	// GET formulaire de création de branche — même garde d'écriture que le POST /branch.
	r.Get("/forum/{slug}/new-branch",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumNewBranchForm(deps)).ServeHTTP)
	r.Get("/forum/{slug}/detail", handleForumDetail(deps))
	r.Post("/forum/{slug}/read", handleForumRead(deps))
	r.Get("/forum/{slug}", handleForumNode(deps))
	r.Post("/forum/{slug}/reply",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumReply(deps)).ServeHTTP)
	// Création d'une question (enfant d'une catégorie) — même garde d'écriture que la réponse.
	r.Post("/forum/{slug}/question",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumCreateQuestion(deps)).ServeHTTP)
	// Création d'une branche (enfant d'une question) — même garde d'écriture.
	r.Post("/forum/{slug}/branch",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumCreateBranch(deps)).ServeHTTP)
	// Renommage d'un nœud générique (catégorie/question/branche) — garde d'écriture.
	r.Post("/forum/{slug}/rename",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumRename(deps)).ServeHTTP)
	// Suppression en cascade (soft-delete) d'un nœud générique — garde d'écriture.
	r.Post("/forum/{slug}/delete",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumDelete(deps)).ServeHTTP)
	// Pièces jointes forum : route gardée (visibilité nœud porteur + journal d'accès).
	// Doit être enregistrée AVANT le FileServer /static/uploads/* monté par api.New.
	r.Get("/forum/piece/{slug}/{filename}", handleForumPieceDownload(deps))
	r.Get("/forum/piece/{filename}", handleForumPieceDownload(deps))
	// Retrait du service statique des uploads forum : l'ancienne URL rend 404.
	r.HandleFunc("/static/uploads/forum/*", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// Node générique
	r.Get("/n/{slug}", handleNodeViewer(deps, treeStore))

	// Search
	r.Get("/search", handleSearch(deps))

	// Auth (login/register/logout)
	r.Get("/login", handleLoginPage(deps))
	r.Post("/login", handleLoginSubmit(deps))
	r.Get("/register", handleRegisterPage(deps))
	r.Post("/register", handleRegisterSubmit(deps))
	// Logout : POST seulement (anti-CSRF). Le logout par GET expose à un
	// logout-CSRF (ex. <img src="/logout"> sur un site tiers) ; la déconnexion
	// est une action de changement d'état et doit passer par POST + token CSRF.
	// Le formulaire est rendu par csrf.js qui injecte automatiquement le token.
	r.Post("/logout", handleLogout)
	// Password reset flow (M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW)
	r.Get("/forgot", handleForgotForm(deps))
	r.Post("/forgot", handleForgotSubmit(deps))
	r.Get("/reset", handleResetForm(deps))
	r.Post("/reset", handleResetSubmit(deps))
	// Aliases /forgot-password et /reset-password déjà redirect 301 vers /forgot et /forgot.
	// On override /reset-password pour rediriger vers /reset?token=... si token query présent.
	r.Get("/reset-password", func(w http.ResponseWriter, req *http.Request) {
		target := "/reset"
		if t := req.URL.Query().Get("token"); t != "" {
			target = "/reset?token=" + t
		}
		http.Redirect(w, req, target, http.StatusMovedPermanently)
	})

	// Messagerie privée membre↔membre (S4). Gating auth dans les handlers ;
	// mutations partagent le Store des actions registry (LLM-parité).
	r.Get("/messages", handleMessagesInbox(deps))
	r.Get("/messages/{userID}", handleMessagesThread(deps))
	r.Post("/messages/{userID}", handleMessagesSend(deps))

	// Events / agenda (S4) — liste + fiche publiques ; création/suppression via
	// le registry (HTTP admin générique + MCP), conforme au périmètre v1.
	r.Get("/events", handleEventsList(deps))
	r.Get("/events/{slug}", handleEventDetail(deps))

	// Cotisations / adhésions (S4) — gestion admin + vue membre.
	r.With(requireAdmin).Get("/admin/memberships", handleAdminMemberships(deps))
	r.With(requireAdmin).Post("/admin/memberships", handleAdminMemberships(deps))
	r.With(requireAdmin).Post("/admin/memberships/status", handleAdminMembershipStatus(deps))
	r.Get("/account/membership", handleMyMembership(deps))

	// Registre parcellaire AFP (cadastre) — gestion admin + vue membre filtrée
	// par appartenance (un membre ne voit que les parcelles sur lesquelles il
	// détient un droit, via ListForUser).
	r.With(requireAdmin).Get("/admin/parcelles", handleAdminParcelles(deps))
	r.With(requireAdmin).Post("/admin/parcelles", handleAdminParcelles(deps))
	r.With(requireAdmin).Post("/admin/parcelles/droit", handleAdminParcelleDroit(deps))
	// Garde d'authentification de niveau route (patron requireAuth, jumeau de
	// requireAdmin). La défense u == nil dans handleMyParcelles reste en
	// ceinture-et-bretelles.
	// Vue membre du module « parcelles » : montée seulement si le module n'est pas
	// désactivé pour l'instance. Désactivé → route non enregistrée, 404 naturel, une
	// instance verticale (ex. DEMOAPP) sert alors son propre tableur cadastral à la même
	// place (parcelles propriétaire + preneur, ajout manuel + import CSV).
	if !moduleDisabled(deps.DisabledModules, "/account/parcelles") {
		r.With(withAccountAuth(deps, "/account/parcelles")...).Get("/account/parcelles", handleMyParcelles(deps))
	}

	// Conventions de pâturage AFP (C4) — gestion admin (liste, formulaire guidé,
	// génération du document imprimable, révocation) et vue preneur filtrée par
	// appartenance (un membre ne voit que ses propres conventions, via ListForUser).
	r.With(requireAdmin).Get("/admin/conventions", handleAdminConventions(deps))
	r.With(requireAdmin).Post("/admin/conventions", handleAdminConventions(deps))
	r.With(requireAdmin).Get("/admin/conventions/{id}/genere", handleAdminConventionGenere(deps))
	r.With(requireAdmin).Post("/admin/conventions/{id}/revoke", handleAdminConventionRevoke(deps))
	// Vue membre du module « conventions » : montée seulement si le module n'est pas
	// désactivé pour l'instance. Désactivé → route non enregistrée, 404 naturel, une
	// instance verticale (ex. DEMOAPP) sert alors sa propre vue à la même place.
	if !moduleDisabled(deps.DisabledModules, "/account/conventions") {
		r.With(requireAuth).Get("/account/conventions", handleMyConventions(deps))
	}

	// Consultation du corpus juridique AFP (V1b) — recherche brute sourcée dans le
	// corpus de droit (afp_droit), réservée aux membres authentifiés. Le moteur réel
	// est injecté à la bordure (CorpusProvider) ; le défaut est inerte (aucun résultat
	// fabriqué). Lecture pure : aucune mutation.
	r.With(requireAuth).Get("/account/corpus", handleCorpusConsult(deps))

	// Espace propriétaire membre (V3a) — accueil consolidé (résumés + liens vers les
	// vues existantes), cotisations (lecture seule), attestation d'adhésion
	// imprimable, et déclaration de mutation (vente/succession). Toutes les vues sont
	// rôle-scopées sur le membre connecté (requireAuth) ; aucune n'est publique. La
	// liste des déclarations de mutation est aussi consultable côté administration.
	r.With(requireAuth).Get("/account", handleAccountHome(deps))
	r.With(requireAuth).Get("/account/profils", handleAccountProfils(deps))
	r.With(requireAuth).Post("/account/profils/demande", handleAccountProfilsDemande(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/gouvernance", handleAccountGouvernance(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/gouvernance/{id}/pv", handleAccountGouvernancePV(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Get("/account/profils/octroi", handleAccountProfilsOctroi(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Post("/account/profils/octroi", handleAccountProfilsOctroi(deps))
	r.With(requireAuth, requireMetierGrade(deps.ProfileGrant.GovernanceGradeName)).Post("/account/profils/retrait", handleAccountProfilsRetrait(deps))
	r.With(requireAuth).Get("/account/cotisations", handleAccountCotisations(deps))
	r.With(requireAuth).Get("/account/attestation", handleAccountAttestation(deps))
	r.With(withAccountAuth(deps, "/account/mutation")...).Get("/account/mutation", handleAccountMutation(deps))
	r.With(withAccountAuth(deps, "/account/mutation")...).Post("/account/mutation", handleAccountMutation(deps))
	r.With(requireAdmin).Get("/admin/mutations", handleAdminMutations(deps))

	// Espace éleveur/preneur (V3b) — accueil élevage (conventions + parcelles mises
	// à disposition + redevance en lecture seule), déclaration d'entretien réalisé,
	// signalement de problème terrain, export PAC (RPG). Toutes rôle-scopées sur le
	// membre connecté (requireAuth). Le journal de terrain consolidé est consultable
	// côté administration.
	r.With(withAccountAuth(deps, "/account/elevage")...).Get("/account/elevage", handleAccountElevage(deps))
	r.With(withAccountAuth(deps, "/account/elevage/entretien")...).Get("/account/elevage/entretien", handleAccountElevageEntretien(deps))
	r.With(withAccountAuth(deps, "/account/elevage/entretien")...).Post("/account/elevage/entretien", handleAccountElevageEntretien(deps))
	r.With(withAccountAuth(deps, "/account/elevage/probleme")...).Get("/account/elevage/probleme", handleAccountElevageProbleme(deps))
	r.With(withAccountAuth(deps, "/account/elevage/probleme")...).Post("/account/elevage/probleme", handleAccountElevageProbleme(deps))
	r.With(withAccountAuth(deps, "/account/elevage/export-pac.csv")...).Get("/account/elevage/export-pac.csv", handleAccountElevageExportPAC(deps))
	r.With(requireAdmin).Get("/admin/field-reports", handleAdminFieldReports(deps))

	// Parcours guidés AFP (V3c) — intégration d'un propriétaire en cascade (compte +
	// adhésion + parcelles), demande de mise à disposition rôle-scopée sur le membre
	// preneur, et conversion administrative d'une demande en convention. Les parcours
	// orchestrent les stores existants (identity/membership/parcelle/convention/
	// demande) sans recoder aucune entité.
	r.With(requireAdmin).Get("/admin/onboard-proprietaire", handleAdminOnboardProprietaire(deps))
	r.With(requireAdmin).Post("/admin/onboard-proprietaire", handleAdminOnboardProprietaire(deps))
	r.With(requireAuth).Get("/account/demande-mad", handleAccountDemandeMad(deps))
	r.With(requireAuth).Post("/account/demande-mad", handleAccountDemandeMad(deps))
	r.With(requireAdmin).Get("/admin/demandes", handleAdminDemandes(deps))
	r.With(requireAdmin).Get("/admin/demandes/{id}/convertir", handleAdminDemandeConvertir(deps))
	r.With(requireAdmin).Post("/admin/demandes/{id}/convertir", handleAdminDemandeConvertir(deps))

	// Gouvernance associative — socle réunion (V2a). Création d'assemblées générales
	// et convocation des adhérents actifs (courriel + trace). Réservé aux administrateurs.
	r.With(requireAdmin).Get("/admin/governance", handleAdminGovernance(deps))
	r.With(requireAdmin).Post("/admin/governance", handleAdminGovernance(deps))
	r.With(requireAdmin).Post("/admin/governance/{id}/convoke", handleAdminGovernanceConvoke(deps))
	// Présence et représentation (V2b) : écran d'émargement + quorum, marquage présent.
	r.With(requireAdmin).Get("/admin/governance/{id}/attendance", handleAdminGovernanceAttendance(deps))
	r.With(requireAdmin).Post("/admin/governance/{id}/sign", handleAdminGovernanceSign(deps))
	// Décision : vote simple (V2c) : ouverture de résolutions, bulletins, clôture, dépouillement.
	r.With(requireAdmin).Get("/admin/governance/{id}/resolutions", handleAdminGovernanceResolutions(deps))
	r.With(requireAdmin).Post("/admin/governance/{id}/resolutions", handleAdminGovernanceResolutions(deps))
	r.With(requireAdmin).Post("/admin/governance/resolutions/{rid}/vote", handleAdminGovernanceVote(deps))
	r.With(requireAdmin).Post("/admin/governance/resolutions/{rid}/close", handleAdminGovernanceCloseResolution(deps))
	// Trace : procès-verbal et registre append-only (V2d) : consignation des
	// délibérations closes, génération du procès-verbal (immuable).
	r.With(requireAdmin).Get("/admin/governance/{id}/pv", handleAdminGovernancePV(deps))
	r.With(requireAdmin).Post("/admin/governance/{id}/minutes", handleAdminGovernanceGenerateMinutes(deps))
	r.With(requireAdmin).Post("/admin/governance/resolutions/{rid}/record", handleAdminGovernanceRecordDeliberation(deps))

	// Couche RGPD transverse : page publique de politique de données (rendue depuis
	// les const Go) et export des données nominatives du membre courant.
	r.Get("/donnees-personnelles", handleDataPolicy(deps))
	r.With(requireAuth).Get("/account/data-download", handleDataDownload(deps))

	// Newsletter / diffusion (S4) — compose + envoi aux membres actifs (admin).
	r.With(requireAdmin).Get("/admin/newsletters", handleAdminNewsletters(deps))
	r.With(requireAdmin).Post("/admin/newsletters", handleAdminNewsletterCompose(deps))
	r.With(requireAdmin).Post("/admin/newsletters/{id}/send", handleAdminNewsletterSend(deps))

	// Feedback widget
	r.Get("/feedback/form", handleFeedbackForm(deps))
	r.Post("/feedback", handleFeedbackPost(deps, feedbackRL))

	// Webhooks PUBLICS (HelloAsso/Stripe/etc.) : PAS de Bearer/session, HMAC obligatoire.
	// HelloAsso POST depuis ses serveurs avec X-HelloAsso-Signature, sans token user.
	// Si Vault/connectors absent (ASSOKIT_MASTER_KEY pas set), 503 explicite + Retry-After
	// (HelloAsso retry pattern récupère après config admin).
	// M-ASSOKIT-HELLOASSO-WEBHOOK-PUBLIC-ENDPOINT.
	r.Post("/webhooks/{provider}", func(w http.ResponseWriter, req *http.Request) {
		if deps.WebhookReceiver == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"webhook_receiver_not_configured","retry_after":3600}`))
			return
		}
		deps.WebhookReceiver(w, req)
	})

	// Tableau de bord de gestion — point d'entrée admin listant toutes les
	// fonctions + les espaces membre (tester le point de vue de chaque profil).
	r.With(requireAdmin).Get("/admin", handleAdminDashboard(deps, cfg.adminDashboardExtra))

	// Admin Setup — diagnostic de configuration (lecture seule + liens d'édition).
	r.With(requireAdmin).Get("/admin/setup", handleAdminSetup(deps))

	// Admin feedbacks
	r.With(requireAdmin).Get("/admin/feedbacks", handleAdminFeedbackList(deps))
	r.With(requireAdmin).Get("/admin/feedbacks/{id}", handleAdminFeedbackDetail(deps))
	r.With(requireAdmin).Post("/admin/feedbacks/{id}/triage", handleAdminFeedbackTriage(deps))

	// Admin donations (M-ASSOKIT-SPRINT3-S4) — UI Boris dons + stats + export CSV.
	r.With(requireAdmin).Get("/admin/donations", AdminDonationsList(deps))
	r.With(requireAdmin).Get("/admin/donations/stats.json", AdminDonationsStats(deps))
	r.With(requireAdmin).Get("/admin/donations/export.csv", AdminDonationsExportCSV(deps))
	r.With(requireAdmin).Get("/admin/donations/{id}", AdminDonationDetail(deps))
	r.With(requireAdmin).Post("/admin/donations/{id}/erase-email", AdminDonationSoftEraseEmail(deps))
	r.With(requireAdmin).Post("/admin/donations/{id}/match-user", AdminDonationManualUserMatch(deps))

	// Admin RBAC — routes protégées par perms.Required via middleware RBAC.
	// Réutilise l'UNIQUE Service applicatif (deps.RBAC) pour que Can() ici, le MCP
	// et les actions de mutation partagent le même cache L1 (cohérence d'invalidation).
	rbacSvc := deps.RBAC
	if rbacSvc == nil {
		rbacSvc = &svcrbac.Service{
			Store: &svcrbac.Store{DB: deps.DB},
			Cache: &svcrbac.Cache{},
		}
	}
	r.Group(func(r chi.Router) {
		r.Use(requireRBACAdmin(rbacSvc))
		r.Use(middleware.RBAC(rbacSvc))
		mountRBACAdminRoutes(r, deps, rbacSvc)
	})

	_ = permsStore

	// OAuth 2.1 — provider OIDC + consent + social login
	issuer := deps.Config.BaseURL
	if issuer == "" {
		issuer = "http://localhost:8080"
	}
	// Fail-fast : en prod, un issuer HTTP expose les tokens OAuth au réseau
	// (sniffing, rejeu). Le mode HTTP est autorisé uniquement en dev/localhost.
	if deps.Config.Prod && strings.HasPrefix(issuer, "http://") && !deps.Config.OAuthAllowInsecure {
		return fmt.Errorf("MountRoutes: BaseURL http:// interdit en production (activer HTTPS ou définir OAUTH_ALLOW_INSECURE=true pour tests locaux)")
	}
	signingKey := deps.Config.CookieSecret
	if len(deps.Config.OAuthSigningKey) > 0 {
		signingKey = deps.Config.OAuthSigningKey
	} else if deps.Logger != nil {
		deps.Logger.Warn("OAuthSigningKey absent — tokens OAuth invalidés au restart (fallback CookieSecret)")
	}
	allowInsecure := deps.Config.OAuthAllowInsecure || strings.HasPrefix(issuer, "http://")
	oauthHandler, oauthStore, err := intoauth.NewProvider(deps.DB, issuer, signingKey, allowInsecure, &svcrbac.Store{DB: deps.DB})
	if err != nil {
		// Fail-loud : sans /oauth2 (authorize/token), monter /oauth2/register et /mcp
		// produirait un serveur OAuth amputé et incohérent. On refuse le démarrage.
		return fmt.Errorf("MountRoutes: init OAuth provider: %w", err)
	}
	mountOAuthRoutes(r, deps, oauthHandler, oauthStore)

	// OAuth2 Dynamic Client Registration RFC 7591 — POST /oauth2/register public.
	// Permet à claude.ai web et autres clients MCP standards de s'auto-register.
	r.Post("/oauth2/register", OAuth2RegisterHandler(deps))

	// /.well-known/oauth-authorization-server enrichi RFC 8414 + DCR + PKCE.
	// Enregistré AVANT mountMCPEndpoint (qui avait définition partielle).
	r.Get("/.well-known/oauth-authorization-server", WellKnownOAuthAuthorizationServer(deps))

	// MCP Streamable HTTP — endpoint /mcp + discovery /.well-known/mcp/server
	// Le vault LiveKit est passé pour enregistrer room.join si disponible (L2a).
	if err := mountMCPEndpoint(r, deps, rbacSvc, cfg.registerActions, cfg.liveKitVault); err != nil {
		return err
	}

	// Admin panel branding V0 — 25 champs + auto-save
	adminpanel.Mount(r, deps)

	// Salon de discussion L3+L4 — liste, chat polling HTMX, create, join, toggle-visio, enclave visio.
	MountSalonRoutes(r, deps, cfg.liveKitVault)

	return nil
}

// newNullString crée un sql.NullString non-nul.
func newNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
