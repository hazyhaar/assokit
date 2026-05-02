// CLAUDE:SUMMARY MountRoutes — câble toutes les routes NPS sur le router chi.
package handlers

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	adminpanel "github.com/hazyhaar/assokit/internal/handlers/admin_panel"
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
	r.Get("/mentions-legales", handlePage(deps, "mentions-legales", treeStore))
	merciHandlerImpl = makeMerciHandler(deps)
	notFoundHandlerImpl = makeNotFoundHandler(deps)
	r.Get("/merci", handleMerci)
	r.Get("/404", handleNotFound)
	r.NotFound(makeNotFoundHandler(deps))

	// Soutenir / Contact
	r.Get("/soutenir", handleDonatePage(deps))
	// Aliases liens-externes/branding pour éviter 404 (prod NPS branding.toml + layout.templ buttons).
	r.Get("/donate", func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/soutenir", http.StatusMovedPermanently) })
	r.Get("/signup", func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/participer", http.StatusMovedPermanently) })
	r.Get("/forgot-password", func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/forgot", http.StatusMovedPermanently) })
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
	r.Get("/forum/new", handleForumNewTopicForm(deps))
	r.Post("/forum/new", handleForumCreateTopic(deps))
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

	// Feedback widget
	r.Get("/feedback/form", handleFeedbackForm(deps))
	r.Post("/feedback", handleFeedbackPost(deps, feedbackRL))

	// Webhooks PUBLICS (HelloAsso/Stripe/etc.) : PAS de Bearer/session, HMAC obligatoire.
	// HelloAsso POST depuis ses serveurs avec X-HelloAsso-Signature, sans token user.
	// Si Vault/connectors absent (NPS_MASTER_KEY pas set), 503 explicite + Retry-After
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

	// OAuth2 Dynamic Client Registration RFC 7591 — POST /oauth2/register public.
	// Permet à claude.ai web et autres clients MCP standards de s'auto-register.
	r.Post("/oauth2/register", OAuth2RegisterHandler(deps))

	// /.well-known/oauth-authorization-server enrichi RFC 8414 + DCR + PKCE.
	// Enregistré AVANT mountMCPEndpoint (qui avait définition partielle).
	r.Get("/.well-known/oauth-authorization-server", WellKnownOAuthAuthorizationServer(deps))

	// MCP Streamable HTTP — endpoint /mcp + discovery /.well-known/mcp/server
	mountMCPEndpoint(r, deps, rbacSvc)

	// Admin panel branding V0 — 25 champs + auto-save
	adminpanel.Mount(r, deps)
}

// newNullString crée un sql.NullString non-nul.
func newNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
