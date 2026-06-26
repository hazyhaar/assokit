// CLAUDE:SUMMARY Handlers salon L3 : liste, page chat (polling HTMX 3s), create, join, toggle-visio, fragment messages.
package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/salon"
)

// MountSalonRoutes câble les routes salon sur r.
// Appelé depuis MountRoutes (routes.go) après les routes existantes.
// vault est optionnel : nil si LiveKit n'est pas configuré (la route visio répond 503).
func MountSalonRoutes(r chi.Router, deps app.AppDeps, vault *assets.Vault) {
	r.Get("/salons", handleSalonList(deps))
	r.Post("/salons", handleSalonCreate(deps))
	r.Post("/salons/join", handleSalonJoin(deps))

	r.Get("/salon/{slug}", handleSalonPage(deps))
	r.Post("/salon/{slug}/messages", handleSalonPostMessage(deps))
	r.Get("/salon/{slug}/messages/fragment", handleSalonMessagesFragment(deps))
	r.Post("/salon/{slug}/toggle-visio", handleSalonToggleVisio(deps))

	// Route enclave visio L4 : GET + routes de modération hôte POST.
	MountSalonVisioRoute(r, deps, vault)
	// Routes comptes-rendus T5 : liste, détail, téléchargement.
	MountTranscriptRoutes(r, deps)
}

// handleSalonList affiche la liste des salons dont l'utilisateur est membre.
func handleSalonList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login?redirect_uri=/salons", http.StatusFound)
			return
		}
		store := &salon.Store{DB: deps.DB}
		salons, err := store.List(r.Context(), u.ID)
		if err != nil {
			deps.Logger.Error("salon_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, "Salons", views.SalonList(salons, csrfToken))
	}
}

// handleSalonCreate traite POST /salons : crée un salon et redirige vers sa page.
func handleSalonCreate(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		password := r.FormValue("password")
		visioEnabled := r.FormValue("visio_enabled") == "1"

		store := &salon.Store{DB: deps.DB}
		sl, err := store.Create(r.Context(), name, u.ID, password, visioEnabled)
		if err != nil {
			deps.Logger.Error("salon_create", "err", err.Error())
			middleware.PushFlash(w, "error", "Erreur lors de la création du salon : "+err.Error())
			http.Redirect(w, r, "/salons", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/salon/"+sl.Slug, http.StatusSeeOther)
	}
}

// handleSalonJoin traite POST /salons/join : ajoute l'utilisateur comme membre.
// Si le salon est protégé et que le mot de passe est absent, affiche le formulaire de saisie.
func handleSalonJoin(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		slug := r.FormValue("slug")
		password := r.FormValue("password")

		store := &salon.Store{DB: deps.DB}
		sl, err := store.Get(r.Context(), slug)
		if err != nil {
			if errors.Is(err, salon.ErrNotFound) {
				middleware.PushFlash(w, "error", "Salon introuvable.")
				http.Redirect(w, r, "/salons", http.StatusSeeOther)
				return
			}
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		// Salon protégé sans mot de passe soumis : afficher le formulaire de saisie.
		if sl.PasswordHash.Valid && password == "" {
			csrfToken := middleware.CSRFToken(r.Context())
			renderPageV2(w, r, deps, "Rejoindre "+sl.Name, views.SalonJoinForm(sl, "", csrfToken))
			return
		}

		err = store.Join(r.Context(), slug, u.ID, password)
		if err != nil {
			switch {
			case errors.Is(err, salon.ErrAlreadyMember):
				// Déjà membre : redirige vers le salon sans erreur.
				http.Redirect(w, r, "/salon/"+slug, http.StatusSeeOther)
				return
			case errors.Is(err, salon.ErrWrongPassword):
				csrfToken := middleware.CSRFToken(r.Context())
				renderPageV2(w, r, deps, "Rejoindre "+sl.Name, views.SalonJoinForm(sl, "Mot de passe incorrect.", csrfToken))
				return
			case errors.Is(err, salon.ErrArchived):
				middleware.PushFlash(w, "error", "Ce salon est archivé.")
				http.Redirect(w, r, "/salons", http.StatusSeeOther)
				return
			default:
				deps.Logger.Error("salon_join", "err", err.Error())
				http.Error(w, "Erreur interne", http.StatusInternalServerError)
				return
			}
		}
		http.Redirect(w, r, "/salon/"+slug, http.StatusSeeOther)
	}
}

// handleSalonPage affiche la page d'un salon (en-tête + chat + formulaire).
// Garde : l'utilisateur doit être membre, sinon 403.
func handleSalonPage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		slug := chi.URLParam(r, "slug")
		store := &salon.Store{DB: deps.DB}

		sl, err := store.Get(r.Context(), slug)
		if err != nil {
			if errors.Is(err, salon.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		ok, err := store.IsMember(r.Context(), slug, u.ID)
		if err != nil {
			deps.Logger.Error("salon_page_member_check", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "Accès réservé aux membres du salon.", http.StatusForbidden)
			return
		}

		isOwner, err := store.IsOwner(r.Context(), slug, u.ID)
		if err != nil {
			deps.Logger.Error("salon_page_owner_check", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		msgs, err := store.Messages(r.Context(), slug, 50)
		if err != nil {
			deps.Logger.Error("salon_page_messages", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		authorNames := resolveAuthorNames(r.Context(), deps.DB, msgs)
		csrfToken := middleware.CSRFToken(r.Context())
		renderPageV2(w, r, deps, sl.Name, views.SalonPage(sl, msgs, isOwner, authorNames, csrfToken))
	}
}

// handleSalonMessagesFragment sert GET /salon/{slug}/messages/fragment.
// Rend uniquement le contenu intérieur de #salon-messages (pas de shell).
// Garde : membre du salon requis.
func handleSalonMessagesFragment(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "Non authentifié", http.StatusUnauthorized)
			return
		}
		slug := chi.URLParam(r, "slug")
		store := &salon.Store{DB: deps.DB}

		ok, err := store.IsMember(r.Context(), slug, u.ID)
		if err != nil || !ok {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}

		msgs, err := store.Messages(r.Context(), slug, 50)
		if err != nil {
			deps.Logger.Error("salon_fragment_messages", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		authorNames := resolveAuthorNames(r.Context(), deps.DB, msgs)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := middleware.WithPageURI(r.Context(), r.RequestURI)
		if err := views.SalonMessagesFragment(msgs, authorNames).Render(ctx, w); err != nil {
			deps.Logger.Error("salon_fragment_render", "err", err.Error())
		}
	}
}

// handleSalonPostMessage traite POST /salon/{slug}/messages.
// Garde : membre du salon requis.
func handleSalonPostMessage(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		slug := chi.URLParam(r, "slug")
		store := &salon.Store{DB: deps.DB}

		ok, err := store.IsMember(r.Context(), slug, u.ID)
		if err != nil || !ok {
			http.Error(w, "Accès refusé", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		body := r.FormValue("body")
		if _, err := store.PostMessage(r.Context(), slug, u.ID, body); err != nil {
			deps.Logger.Error("salon_post_message", "slug", slug, "err", err.Error())
			middleware.PushFlash(w, "error", "Erreur lors de l'envoi du message.")
		}
		http.Redirect(w, r, "/salon/"+slug, http.StatusSeeOther)
	}
}

// handleSalonToggleVisio traite POST /salon/{slug}/toggle-visio.
// Garde : owner uniquement.
func handleSalonToggleVisio(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		slug := chi.URLParam(r, "slug")
		store := &salon.Store{DB: deps.DB}

		isOwner, err := store.IsOwner(r.Context(), slug, u.ID)
		if err != nil {
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		if !isOwner {
			http.Error(w, "Réservé au propriétaire du salon.", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		on := r.FormValue("visio_enabled") == "1"
		if err := store.ToggleVisio(r.Context(), slug, on); err != nil {
			deps.Logger.Error("salon_toggle_visio", "slug", slug, "err", err.Error())
			middleware.PushFlash(w, "error", "Erreur lors de la modification de la visio.")
		}
		http.Redirect(w, r, "/salon/"+slug, http.StatusSeeOther)
	}
}

// resolveAuthorNames construit une map authorID → nom d'affichage
// à partir de la liste de messages, en une seule passe SQL par auteur unique.
func resolveAuthorNames(ctx context.Context, db *sql.DB, msgs []salon.Message) map[string]string {
	ids := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		ids[m.AuthorID] = struct{}{}
	}
	if len(ids) == 0 {
		return nil
	}
	store := &identity.Store{DB: db}
	names := make(map[string]string, len(ids))
	for id := range ids {
		u, err := store.GetByID(ctx, id)
		if err != nil || u == nil {
			continue
		}
		if u.DisplayName != "" {
			names[id] = u.DisplayName
		} else {
			names[id] = u.Email
		}
	}
	return names
}
