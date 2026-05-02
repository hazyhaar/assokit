// CLAUDE:SUMMARY Forum handler : index racine + thread récursif + reply via templ pkg/horui/forum.
package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/forum"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/tree"
)

// ForumMaxDepth : profondeur max autorisée pour les réponses (création).
const ForumMaxDepth = 3

func handleForumIndex(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		forumNode, err := treeStore.GetBySlug(r.Context(), "forum")
		if err != nil {
			deps.Logger.Error("forum index : node racine introuvable", "err", err)
			user := middleware.UserFromContext(r.Context())
			renderPage(w, r, deps, "Forum", forum.Index(nil, user))
			return
		}
		topics, err := forum.BuildIndex(r.Context(), treeStore, forumNode.ID, authorOf)
		if err != nil {
			deps.Logger.Error("forum index build", "err", err)
			http.Error(w, "Erreur lecture forum", http.StatusInternalServerError)
			return
		}
		user := middleware.UserFromContext(r.Context())
		renderPage(w, r, deps, "Forum", forum.Index(topics, user))
	}
}

// handleForumNewTopicForm rend le formulaire de création d'un nouveau sujet racine.
// User authentifié requis. Auto-bootstrap du nœud racine 'forum' si absent.
func handleForumNewTopicForm(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		csrfToken := middleware.CSRFToken(r.Context())
		renderPage(w, r, deps, "Nouveau sujet — Forum", forum.NewTopic(csrfToken))
	}
}

// handleForumCreateTopic crée un nouveau sujet racine (parent_id = node-forum).
// Slug auto-généré depuis title via tree.slugify (collision → suffixe court UUID).
func handleForumCreateTopic(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" || body == "" {
			middleware.PushFlash(w, "error", "Titre et message obligatoires.")
			http.Redirect(w, r, "/forum/new", http.StatusSeeOther)
			return
		}
		if len(title) > 180 {
			middleware.PushFlash(w, "error", "Titre trop long (max 180 caractères).")
			http.Redirect(w, r, "/forum/new", http.StatusSeeOther)
			return
		}

		// Auto-bootstrap : crée node racine 'forum' si absent (rule SEED-FORUM-ROOT-AUTO-BOOTSTRAP).
		forumRoot, err := ensureForumRoot(r.Context(), treeStore)
		if err != nil {
			deps.Logger.Error("forum create: ensureForumRoot", "err", err)
			http.Error(w, "Erreur initialisation forum", http.StatusInternalServerError)
			return
		}

		// Création du nœud, retry sur slug collision avec suffixe court.
		newID, err := treeStore.Create(r.Context(), tree.Node{
			ParentID: sql.NullString{String: forumRoot.ID, Valid: true},
			Type:     "post",
			Title:    title,
			BodyMD:   body,
			AuthorID: sql.NullString{String: user.ID, Valid: true},
		})
		if err != nil && err == tree.ErrSlugTaken {
			// Retry avec slug suffixé (uuid 8 chars).
			newID, err = treeStore.Create(r.Context(), tree.Node{
				Slug:     "topic-" + uuid.New().String()[:8],
				ParentID: sql.NullString{String: forumRoot.ID, Valid: true},
				Type:     "post",
				Title:    title,
				BodyMD:   body,
				AuthorID: sql.NullString{String: user.ID, Valid: true},
			})
		}
		if err != nil {
			deps.Logger.Error("forum create topic", "err", err)
			middleware.PushFlash(w, "error", "Erreur création du sujet.")
			http.Redirect(w, r, "/forum/new", http.StatusSeeOther)
			return
		}

		// Récupère le slug effectif pour redirect.
		newNode, err := getNodeByID(r.Context(), deps.DB, newID)
		if err != nil || newNode == nil {
			http.Redirect(w, r, "/forum", http.StatusSeeOther)
			return
		}
		deps.Logger.Info("forum_topic_created", "user_id", user.ID, "node_id", newID, "slug", newNode.Slug)
		http.Redirect(w, r, "/forum/"+newNode.Slug, http.StatusSeeOther)
	}
}

// ensureForumRoot retourne le node racine 'forum'. Le crée si absent
// (auto-bootstrap : pas de dépendance manuelle SQL pour démarrer le forum).
func ensureForumRoot(ctx context.Context, store *tree.Store) (*tree.Node, error) {
	if n, err := store.GetBySlug(ctx, "forum"); err == nil {
		return n, nil
	}
	id, err := store.Create(ctx, tree.Node{
		Slug:       "forum",
		Type:       "folder",
		Title:      "Forum",
		Visibility: "public",
	})
	if err != nil {
		return nil, fmt.Errorf("ensureForumRoot create: %w", err)
	}
	return store.GetByID(ctx, id)
}

// getNodeByID lit un node par ID via une query directe (le store n'expose qu'un GetByID
// de tree.Store ; ce wrapper évite de re-construire le store ici).
func getNodeByID(ctx context.Context, db *sql.DB, id string) (*tree.Node, error) {
	store := &tree.Store{DB: db}
	return store.GetByID(ctx, id)
}

func handleForumNode(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		node, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		root, err := forum.BuildThread(r.Context(), treeStore, *node, authorOf, 0)
		if err != nil {
			deps.Logger.Error("forum node build root", "err", err)
			http.Error(w, "Erreur lecture sujet", http.StatusInternalServerError)
			return
		}
		replies, err := childThreads(r.Context(), treeStore, node.ID, authorOf, forum.MaxLoadDepth)
		if err != nil {
			deps.Logger.Error("forum node build replies", "err", err)
			http.Error(w, "Erreur lecture réponses", http.StatusInternalServerError)
			return
		}
		user := middleware.UserFromContext(r.Context())
		canReply := user != nil && node.Depth < ForumMaxDepth-1
		csrfToken := middleware.CSRFToken(r.Context())
		renderPage(w, r, deps, node.Title, forum.Thread(root, replies, user, canReply, csrfToken))
	}
}

func handleForumReply(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		slug := chi.URLParam(r, "slug")
		parent, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if parent.Depth >= ForumMaxDepth-1 {
			http.Error(w, "Profondeur de fil maximale atteinte.", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Formulaire invalide", http.StatusBadRequest)
			return
		}
		title := r.FormValue("title")
		body := r.FormValue("body")
		if title == "" || body == "" {
			middleware.PushFlash(w, "error", "Titre et message obligatoires.")
			http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
			return
		}
		replySlug := "reply-" + uuid.New().String()[:8]
		_, err = treeStore.Create(r.Context(), tree.Node{
			Slug:     replySlug,
			ParentID: sql.NullString{String: parent.ID, Valid: true},
			Type:     "post",
			Title:    title,
			BodyMD:   body,
			BodyHTML: body,
			AuthorID: sql.NullString{String: user.ID, Valid: true},
		})
		if err != nil {
			deps.Logger.Error("forum reply create", "err", err)
			middleware.PushFlash(w, "error", "Erreur création réponse.")
			http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
	}
}

func childThreads(ctx context.Context, store *tree.Store, parentID string, authorOf func(ctx context.Context, userID string) string, maxDepth int) ([]forum.ThreadNode, error) {
	kids, err := store.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]forum.ThreadNode, 0, len(kids))
	for _, k := range kids {
		tn, err := forum.BuildThread(ctx, store, k, authorOf, maxDepth-1)
		if err != nil {
			return nil, err
		}
		out = append(out, tn)
	}
	return out, nil
}

func authorResolver(db *sql.DB) func(ctx context.Context, userID string) string {
	store := &auth.Store{DB: db}
	return func(ctx context.Context, userID string) string {
		if userID == "" {
			return ""
		}
		u, err := store.GetByID(ctx, userID)
		if err != nil || u == nil {
			return ""
		}
		return u.DisplayName
	}
}
