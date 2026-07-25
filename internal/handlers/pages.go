// CLAUDE:SUMMARY Handlers pages (vague 2 : renderPageV2 + views.*  pour toutes les pages).
package handlers

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/search"
	tree "github.com/hazyhaar/nodetree"
)

func handlePage(deps app.AppDeps, slug string, s *tree.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if slug == "home" {
			// Une instance peut surcharger la page d'accueil en gravant une
			// page CMS de slug "home" : si elle existe, elle est servie en
			// priorité. Sinon, repli sur la vitrine générique views.Home (templux v1).
			if node, err := s.GetBySlug(r.Context(), "home"); err == nil && node != nil && node.BodyHTML != "" {
				renderPageV2(w, r, deps, node.Title, views.StaticPage(node.Title, "", node.BodyHTML, ""))
				return
			}
			renderPageV2(w, r, deps, "Accueil", views.Home())
			return
		}
		node, err := s.GetBySlug(r.Context(), slug)
		title := pageTitleFromSlug(slug)
		if err != nil || node == nil {
			renderPageV2(w, r, deps, title, views.StaticPage(title, sectionLabelFromSlug(slug), "", pageAdminEditPath(r, slug)))
			return
		}
		renderPageV2(w, r, deps, node.Title, views.StaticPage(node.Title, sectionLabelFromSlug(slug), node.BodyHTML, pageAdminEditPath(r, slug)))
	}
}

func handleThematique(deps app.AppDeps, s *tree.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		node, err := s.GetBySlug(r.Context(), slug)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		children, _ := s.Children(r.Context(), node.ID)
		renderPageV2(w, r, deps, node.Title, views.Thematique(*node, children, pageAdminEditPath(r, slug)))
	}
}

func handleNodeViewer(deps app.AppDeps, s *tree.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		node, err := s.GetBySlug(r.Context(), slug)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		renderPageV2(w, r, deps, node.Title, views.StaticPage(node.Title, "", node.BodyHTML, ""))
	}
}

func handleParticiper(deps app.AppDeps) http.HandlerFunc {
	// Les cartes proviennent du catalogue injecté à la bordure (deps.Profils),
	// jamais d'une liste codée en dur. L'icône reste l'Icon du profil (vide par
	// défaut, donc sans emoji) — la charte proscrit les emoji décoratifs.
	return func(w http.ResponseWriter, r *http.Request) {
		profils := make([]views.ProfileOption, 0, len(deps.Profils))
		for _, p := range deps.Profils {
			profils = append(profils, views.ProfileOption{
				ID:       p.ID,
				Icon:     p.Icon,
				Label:    p.Label,
				Subtitle: p.Subtitle,
			})
		}
		renderPageV2(w, r, deps, "Participer", views.Participer(profils))
	}
}

func handleSearch(deps app.AppDeps) http.HandlerFunc {
	engine := &search.Engine{DB: deps.DB}
	store := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		user := middleware.UserFromContext(r.Context())
		var results []views.SearchResult
		if q != "" {
			hits, err := engine.Query(r.Context(), q, 30)
			if err != nil {
				deps.Logger.Error("search", "q", q, "err", err)
			} else {
				results = make([]views.SearchResult, 0, len(hits))
				for _, h := range hits {
					n, err := store.GetByID(r.Context(), h.NodeID)
					if err != nil || n == nil {
						continue
					}
					// Résultats forum : visibilité effective du lecteur (forumCanRead).
					// Autres types (pages, événements…) : comportement inchangé.
					if isForum, err := isForumTreeNode(r.Context(), store, n.ID); err == nil && isForum {
						if !forumCanRead(r.Context(), deps, user, n.ID) {
							continue
						}
					}
					results = append(results, views.SearchResult{
						Title:   h.Title,
						Slug:    n.Slug,
						Snippet: h.Snippet,
					})
				}
			}
		}
		renderPageV2(w, r, deps, "Recherche", views.PagesSearch(q, results))
	}
}

func handleLoginPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPageV2(w, r, deps, "Connexion", views.Login(middleware.CSRFToken(r.Context())))
	}
}

func handleLoginSubmit(deps app.AppDeps) http.HandlerFunc {
	authStore := &identity.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		email := r.FormValue("email")
		password := r.FormValue("password")

		// Anti-bruteforce : verrou temporaire après trop d'échecs sur ce compte.
		if locked, retry := globalLoginGuard.Locked(email); locked {
			middleware.PushFlash(w, "error", "Trop de tentatives. Réessayez plus tard.")
			deps.Logger.Warn("login_locked_out", "email_hash", middleware.HashEmail(email), "retry_in", retry.String())
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		user, err := authStore.Authenticate(r.Context(), email, password)
		if err != nil {
			globalLoginGuard.RecordFailure(email)
			middleware.PushFlash(w, "error", "Email ou mot de passe incorrect.")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		globalLoginGuard.Reset(email)
		middleware.SetSessionCookie(w, user.ID, deps.Config.CookieSecret, deps.Config.CookieSecure())
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func handleRegisterPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPageV2(w, r, deps, "Créer un compte", views.PagesRegister())
	}
}

func handleRegisterSubmit(deps app.AppDeps) http.HandlerFunc {
	authStore := &identity.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		email := r.FormValue("email")
		password := r.FormValue("password")
		name := r.FormValue("display_name")
		user, err := authStore.Register(r.Context(), email, password, name)
		if err != nil {
			middleware.PushFlash(w, "error", "Erreur lors de la création du compte.")
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}
		middleware.SetSessionCookie(w, user.ID, deps.Config.CookieSecret, deps.Config.CookieSecure())
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

var handleLogout = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	middleware.ClearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
})

// handleForgotStub : laissé en compat pour ancien call-site (orphelin
// après livraison M-ASSOKIT-IMPL-PASSWORD-RESET-FLOW). Le router pointe
// maintenant vers handleForgotForm/Submit. À supprimer dans v0.3.
func handleForgotStub(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/forgot", http.StatusMovedPermanently)
}

func makeMerciHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		title := "Merci"
		message := "Votre demande a bien été enregistrée. Vous recevrez un email de confirmation sous peu."
		renderPageV2(w, r, deps, title, views.PagesMerci(title, message))
	}
}

func makeNotFoundHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		renderPageV2(w, r, deps, "404", views.PagesNotFound())
	}
}

func pageTitleFromSlug(slug string) string {
	switch slug {
	case "charte":
		return "Charte"
	case "thematiques":
		return "Thématiques"
	case "medias":
		return "Médias"
	case "mentions-legales":
		return "Mentions légales"
	case "faq":
		return "Foire aux questions"
	case "lexique":
		return "Lexique"
	default:
		return slug
	}
}

// pageAdminEditPath renvoie le chemin d'édition admin pour une page CMS vide,
// uniquement si l'utilisateur courant est administrateur. Chaîne vide sinon.
func pageAdminEditPath(r *http.Request, slug string) string {
	u := middleware.UserFromContext(r.Context())
	if u == nil || !slices.Contains(u.Roles, "admin") {
		return ""
	}
	switch slug {
	case "charte":
		return "/admin/panel"
	case "faq", "lexique", "thematiques":
		return "/admin/actions/pages.update"
	default:
		return ""
	}
}

func sectionLabelFromSlug(slug string) string {
	switch slug {
	case "charte":
		return "Nos principes"
	case "thematiques":
		return "Thématiques"
	case "medias":
		return "Médias"
	case "faq":
		return "Aide"
	case "lexique":
		return "Aide"
	default:
		return ""
	}
}
