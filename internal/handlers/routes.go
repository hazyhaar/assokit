// CLAUDE:SUMMARY MountRoutes — câble toutes les routes NPS sur le router chi.
package handlers

import (
	"database/sql"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	intoauth "github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	svcrbac "github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

// MountRoutes câble toutes les routes NPS sur r.
func MountRoutes(r chi.Router, deps app.AppDeps) {
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
	merciHandlerImpl = makeMerciHandler(deps)
	notFoundHandlerImpl = makeNotFoundHandler(deps)
	r.Get("/merci", handleMerci)
	r.Get("/404", handleNotFound)
	r.NotFound(makeNotFoundHandler(deps))

	// Soutenir / Contact
	r.Get("/soutenir", handleDonatePage(deps))
	r.Get("/contact", handleContactPage(deps))
	r.Post("/contact", handleContactSubmit(deps))

	// Participer / Adhérer
	r.Get("/participer", handleParticiper(deps))
	r.Get("/adherer/{profil}", handleSignupForm(deps))
	r.Post("/adherer/{profil}", handleSignupSubmit(deps))

	// Activation magic link
	r.Get("/activate/{token}", handleActivate(deps))
	// Forum
	r.Get("/forum", handleForumIndex(deps))
	r.Get("/forum/{slug}", handleForumNode(deps))
	r.Post("/forum/{slug}/reply",
		middleware.RequirePerm(deps.DB, perms.PermWrite, func(r *http.Request) string {
			return "node-forum"
		})(handleForumReply(deps)).ServeHTTP)

	// Node générique
	r.Get("/n/{slug}", handleNodeViewer(deps, treeStore))

	// Search
	r.Get("/search", handleSearch(deps))

	// Auth (login/register/logout)
	r.Get("/login", handleLoginPage(deps))
	r.Post("/login", handleLoginSubmit(deps))
	r.Get("/register", handleRegisterPage(deps))
	r.Post("/register", handleRegisterSubmit(deps))
	r.Post("/logout", handleLogout)
	r.Get("/forgot", handleForgotStub)

	// Feedback widget
	r.Get("/feedback/form", handleFeedbackForm(deps))
	r.Post("/feedback", handleFeedbackPost(deps, feedbackRL))

	// Admin feedbacks
	r.With(requireAdmin).Get("/admin/feedbacks", handleAdminFeedbackList(deps))
	r.With(requireAdmin).Get("/admin/feedbacks/{id}", handleAdminFeedbackDetail(deps))
	r.With(requireAdmin).Post("/admin/feedbacks/{id}/triage", handleAdminFeedbackTriage(deps))

	// Admin RBAC — routes protégées par perms.Required via middleware RBAC
	rbacSvc := &svcrbac.Service{
		Store: &svcrbac.Store{DB: deps.DB},
		Cache: &svcrbac.Cache{},
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
	signingKey := deps.Config.CookieSecret
	if envKey := os.Getenv("OAUTH_SIGNING_KEY"); envKey != "" {
		signingKey = []byte(envKey)
	}
	oauthHandler, oauthStore, err := intoauth.NewProvider(deps.DB, issuer, signingKey, &svcrbac.Store{DB: deps.DB})
	if err == nil {
		mountOAuthRoutes(r, deps, oauthHandler, oauthStore)
	}
}

// newNullString crée un sql.NullString non-nul.
func newNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
