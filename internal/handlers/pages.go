// CLAUDE:SUMMARY Handlers pages : home, statiques (charte/medias), thématiques, participer, search, auth, node viewer.
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/pages"
	"github.com/hazyhaar/assokit/pkg/horui/search"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

func handlePage(deps app.AppDeps, slug string, s *tree.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if slug == "home" {
			renderPage(w, r, deps, "Accueil", pages.Home())
			return
		}
		node, err := s.GetBySlug(r.Context(), slug)
		title := pageTitleFromSlug(slug)
		if err != nil || node == nil {
			renderPage(w, r, deps, title, pages.StaticPage(title, sectionLabelFromSlug(slug), ""))
			return
		}
		renderPage(w, r, deps, node.Title, pages.StaticPage(node.Title, sectionLabelFromSlug(slug), node.BodyHTML))
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
		renderPage(w, r, deps, node.Title, pages.Thematique(*node, children))
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
		renderPage(w, r, deps, node.Title, pages.StaticPage(node.Title, "", node.BodyHTML))
	}
}

func handleParticiper(deps app.AppDeps) http.HandlerFunc {
	profils := []pages.ProfileOption{
		{ID: "adherent", Icon: "🙋", Label: "Adhérent", Subtitle: "Rejoindre l'association"},
		{ID: "lanceur", Icon: "🛡", Label: "Lanceur d'alerte", Subtitle: "Signaler en sécurité"},
		{ID: "media", Icon: "📰", Label: "Média / Journaliste", Subtitle: "Se référencer"},
		{ID: "asso", Icon: "🤝", Label: "Association", Subtitle: "Collectif, ONG"},
		{ID: "expert", Icon: "⚖️", Label: "Expert", Subtitle: "Avocat, médecin, juriste…"},
		{ID: "partenaire", Icon: "🌐", Label: "Partenaire", Subtitle: "Collaboration"},
		{ID: "benevole", Icon: "💪", Label: "Bénévole", Subtitle: "S'impliquer"},
		{ID: "don", Icon: "💛", Label: "Donateur", Subtitle: "Soutenir"},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, deps, "Participer", pages.Participer(profils))
	}
}

func handleSearch(deps app.AppDeps) http.HandlerFunc {
	engine := &search.Engine{DB: deps.DB}
	store := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		var results []pages.SearchResult
		if q != "" {
			hits, err := engine.Query(r.Context(), q, 30)
			if err != nil {
				deps.Logger.Error("search", "q", q, "err", err)
			} else {
				results = make([]pages.SearchResult, 0, len(hits))
				for _, h := range hits {
					n, err := store.GetByID(r.Context(), h.NodeID)
					if err != nil || n == nil {
						continue
					}
					results = append(results, pages.SearchResult{
						Title:   h.Title,
						Slug:    n.Slug,
						Snippet: h.Snippet,
					})
				}
			}
		}
		renderPage(w, r, deps, "Recherche", pages.Search(q, results))
	}
}

func handleLoginPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, deps, "Connexion", pages.Login())
	}
}

func handleLoginSubmit(deps app.AppDeps) http.HandlerFunc {
	authStore := &auth.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		email := r.FormValue("email")
		password := r.FormValue("password")
		user, err := authStore.Authenticate(r.Context(), email, password)
		if err != nil {
			middleware.PushFlash(w, "error", "Email ou mot de passe incorrect.")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		middleware.SetSessionCookie(w, user.ID, deps.Config.CookieSecret, false)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func handleRegisterPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, deps, "Créer un compte", pages.Register())
	}
}

func handleRegisterSubmit(deps app.AppDeps) http.HandlerFunc {
	authStore := &auth.Store{DB: deps.DB}
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
		middleware.SetSessionCookie(w, user.ID, deps.Config.CookieSecret, false)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

var handleLogout = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	middleware.ClearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
})

func handleForgotStub(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Réinitialisation par email à venir. Contactez contact@assokit.org.", http.StatusNotImplemented)
}

func handleMerci(w http.ResponseWriter, r *http.Request) { merciHandlerImpl(w, r) }

var merciHandlerImpl = func(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Merci handler non initialisé", http.StatusInternalServerError)
}

func makeMerciHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		title := "Merci"
		message := "Votre demande a bien été enregistrée. Vous recevrez un email de confirmation sous peu."
		renderPage(w, r, deps, title, pages.Merci(title, message))
	}
}

func handleNotFound(w http.ResponseWriter, r *http.Request) { notFoundHandlerImpl(w, r) }

var notFoundHandlerImpl = func(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "404", http.StatusNotFound)
}

func makeNotFoundHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		renderPage(w, r, deps, "404", pages.NotFound())
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
	default:
		return slug
	}
}

func sectionLabelFromSlug(slug string) string {
	switch slug {
	case "charte":
		return "Nos principes"
	case "thematiques":
		return "Nos combats"
	case "medias":
		return "Apparitions"
	default:
		return ""
	}
}
