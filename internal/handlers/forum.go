// CLAUDE:SUMMARY Forum handler : index racine + thread récursif + reply (vague 2 : renderPageV2 + views.Forum*).
package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/hazyhaar/assokit/pkg/eventsink"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/uid"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/forum"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/theme"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
	tree "github.com/hazyhaar/nodetree"
)

// forumWriteNodeID : nœud racine porteur des permissions d'écriture du forum.
// Toutes les routes de mutation forum (rename/delete/question/branch/reply/new)
// sont gardées via RequirePerm(deps.DB, perms.PermWrite, "node-forum") — cf.
// routes.go. forumCanWrite applique EXACTEMENT le même prédicat côté vue, pour
// qu'un bouton affiché corresponde toujours à une action qui aboutit (et
// inversement, qu'un bouton caché corresponde à un 403 si l'action est forcée).
const forumWriteNodeID = "node-forum"

// forumCanWrite retourne true si l'utilisateur est connecté ET porte au moins la
// permission d'écriture sur le forum. Prédicat UNIQUE réutilisé par les quatre
// panneaux de la station (catégories, questions, branches, détail/messages) —
// corrige l'asymétrie où seul "+ Créer catégorie" échappait à la garde.
func forumCanWrite(ctx context.Context, deps app.AppDeps, user *identity.User) bool {
	if user == nil {
		return false
	}
	ps := &perms.Store{DB: deps.DB}
	can, err := ps.UserCan(ctx, user.Roles, forumWriteNodeID, perms.PermWrite)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Error("forum canWrite", "err", err)
		}
		return false
	}
	return can
}

// forumUserRoles retourne les rôles effectifs du lecteur. Anonyme → rôle « public »
// (même convention que les tests de permissions node×rôle).
func forumUserRoles(user *identity.User) []string {
	if user == nil {
		return []string{"public"}
	}
	return user.Roles
}

// forumCanRead est le prédicat UNIQUE de visibilité d'un nœud forum : au moins
// PermRead sur le nœud (héritage ancêtres via perms.Effective). Réutilisé par la
// route gardée des pièces jointes et le filtre /search sur les résultats forum.
func forumCanRead(ctx context.Context, deps app.AppDeps, user *identity.User, nodeID string) bool {
	ps := &perms.Store{DB: deps.DB}
	can, err := ps.UserCan(ctx, forumUserRoles(user), nodeID, perms.PermRead)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Error("forum canRead", "node_id", nodeID, "err", err)
		}
		return false
	}
	return can
}

var forumPieceFilenameRe = regexp.MustCompile(`^[0-9a-f]{8}-[a-zA-Z0-9._-]+$`)

func forumPieceURL(carrierSlug, name string) string {
	return "/forum/piece/" + carrierSlug + "/" + name
}

// isForumTreeNode indique si le nœud appartient à l'arbre du forum (descendant
// du nœud racine slug « forum »).
func isForumTreeNode(ctx context.Context, store *tree.Store, nodeID string) (bool, error) {
	node, err := store.GetByID(ctx, nodeID)
	if err != nil {
		return false, err
	}
	if node.Slug == "forum" {
		return true, nil
	}
	ancs, err := store.Ancestors(ctx, nodeID)
	if err != nil {
		return false, err
	}
	for _, a := range ancs {
		if a.Slug == "forum" {
			return true, nil
		}
	}
	return false, nil
}

// ForumMaxDepth : profondeur max autorisée pour les réponses (création). La station
// à catégories ajoute un niveau (racine 0 → catégorie 1 → question 2 → réponse 3),
// donc le plafond doit permettre de répondre à une question de profondeur 2.
const ForumMaxDepth = 5

func handleForumIndex(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		forumNode, err := treeStore.GetBySlug(r.Context(), "forum")
		if err != nil {
			deps.Logger.Error("forum index : node racine introuvable", "err", err)
			user := middleware.UserFromContext(r.Context())
			renderPageV2(w, r, deps, theme.ForumLabel(), views.ForumIndex(nil, user, nil, forumCanWrite(r.Context(), deps, user)))
			return
		}
		topics, err := forum.BuildIndex(r.Context(), treeStore, forumNode.ID, authorOf)
		if err != nil {
			deps.Logger.Error("forum index build", "err", err)
			http.Error(w, "Erreur lecture forum", http.StatusInternalServerError)
			return
		}
		user := middleware.UserFromContext(r.Context())
		// Compteur de questions non lues par catégorie (vide si anonyme).
		catIDs := make([]string, 0, len(topics))
		for _, t := range topics {
			catIDs = append(catIDs, t.ID)
		}
		unread, err := forum.UnreadByCategory(r.Context(), deps.DB, userID(user), catIDs)
		if err != nil {
			deps.Logger.Error("forum index unread", "err", err)
			unread = nil
		}
		renderPageV2(w, r, deps, theme.ForumLabel(), views.ForumIndex(topics, user, unread, forumCanWrite(r.Context(), deps, user)))
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
		renderPageV2(w, r, deps, theme.T("forum.new_topic_page_title", "Nouveau sujet — Forum"), views.ForumNewTopic(csrfToken))
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
			// Retry avec slug suffixé d'un UUIDv7 COMPLET. Un préfixe tronqué
			// (les 8 premiers caractères sont l'horodatage UUIDv7) collisionne en
			// boucle de créations même-milliseconde — c'est ce chemin de repli qui
			// re-collisionnait.
			newID, err = treeStore.Create(r.Context(), tree.Node{
				Slug:     "topic-" + uid.New(),
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
		if deps.EventSink != nil {
			_ = deps.EventSink.Emit(r.Context(), eventsink.Event{
				Type: "forum.post.created",
				Payload: map[string]any{
					"node_id": newID, "slug": newNode.Slug, "title": newNode.Title, "author_id": user.ID,
				},
			})
		}
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
		renderPageV2(w, r, deps, node.Title, views.ForumThread(root, replies, user, canReply, csrfToken))
	}
}

// handleForumQuestions rend le panneau central : les questions d'une catégorie.
// Fragment HTMX injecté dans #forum-col-items.
func handleForumQuestions(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		writeForumQuestions(deps, treeStore, authorOf, w, r, chi.URLParam(r, "slug"))
	}
}

// writeForumQuestions compose et écrit le fragment de la colonne des questions
// d'une catégorie (slug). Partagé entre l'affichage du panneau et le re-rendu
// HTMX après création/renommage/suppression d'une question.
func writeForumQuestions(deps app.AppDeps, treeStore *tree.Store, authorOf func(ctx context.Context, userID string) string, w http.ResponseWriter, r *http.Request, slug string) {
	node, err := treeStore.GetBySlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	{
		category, err := forum.BuildThread(r.Context(), treeStore, *node, authorOf, 0)
		if err != nil {
			deps.Logger.Error("forum questions build category", "err", err)
			http.Error(w, "Erreur lecture catégorie", http.StatusInternalServerError)
			return
		}
		questions, err := childThreads(r.Context(), treeStore, node.ID, authorOf, 1)
		if err != nil {
			deps.Logger.Error("forum questions build list", "err", err)
			http.Error(w, "Erreur lecture questions", http.StatusInternalServerError)
			return
		}
		qIDs := make([]string, 0, len(questions))
		for _, q := range questions {
			qIDs = append(qIDs, q.ID)
		}
		qUser := middleware.UserFromContext(r.Context())
		// Une question "a du nouveau" si un message (sous une de ses branches) est plus
		// récent que la lecture de cette branche — traversée à 2 niveaux, pas enfant direct.
		unread, err := forum.UnreadQuestions(r.Context(), deps.DB, userID(qUser), qIDs)
		if err != nil {
			deps.Logger.Error("forum questions unread", "err", err)
			unread = nil
		}
		views.ForumQuestionsColumn(category, questions, unread, forumCanWrite(r.Context(), deps, qUser)).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleForumDetail rend le panneau de droite : la question, son historique
// d'échanges et le formulaire de réponse. Fragment HTMX injecté dans #forum-col-detail.
func handleForumDetail(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		writeForumDetail(deps, treeStore, authorOf, w, r, chi.URLParam(r, "slug"))
	}
}

// writeForumDetail compose et écrit le fragment du panneau de détail pour la
// question d'un slug donné. Partagé entre l'affichage du détail et le retour
// HTMX après une réponse.
func writeForumDetail(deps app.AppDeps, treeStore *tree.Store, authorOf func(ctx context.Context, userID string) string, w http.ResponseWriter, r *http.Request, slug string) {
	node, err := treeStore.GetBySlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	question, err := forum.BuildThread(r.Context(), treeStore, *node, authorOf, 0)
	if err != nil {
		deps.Logger.Error("forum detail build question", "err", err)
		http.Error(w, "Erreur lecture question", http.StatusInternalServerError)
		return
	}
	replies, err := childThreads(r.Context(), treeStore, node.ID, authorOf, forum.MaxLoadDepth)
	if err != nil {
		deps.Logger.Error("forum detail build replies", "err", err)
		http.Error(w, "Erreur lecture réponses", http.StatusInternalServerError)
		return
	}
	user := middleware.UserFromContext(r.Context())
	canWrite := forumCanWrite(r.Context(), deps, user) && node.Depth < ForumMaxDepth-1
	csrfToken := middleware.CSRFToken(r.Context())
	views.ForumQuestionDetail(question, replies, user, canWrite, csrfToken).Render(r.Context(), w) //nolint:errcheck
}

// forumMD rend le markdown des messages de forum SANS WithUnsafe : le HTML brut
// inséré par un membre est ÉCHAPPÉ (anti-XSS stocké). Le corps rendu est ensuite
// émis via @templ.Raw dans la vue — d'où l'exigence absolue d'un rendu sûr ici, à
// l'identique des messages privés et des pages. Régression auditée 2026-06-13
// (la réponse de forum stockait le corps brut en BodyHTML → XSS).
var forumMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// renderForumMD convertit le markdown en HTML sûr. En cas d'erreur de conversion
// (rarissime), retourne une chaîne vide plutôt que le corps brut — jamais de HTML
// non assaini servi.
func renderForumMD(src string) string {
	var b strings.Builder
	if err := forumMD.Convert([]byte(src), &b); err != nil {
		return ""
	}
	return b.String()
}

// handleForumRead : POST /forum/{slug}/read — marque la question lue par
// l'utilisateur courant (upsert idempotent dans node_reads). Jamais sur un GET
// (idempotence HTTP). Anonyme ou nœud introuvable → no-op silencieux.
func handleForumRead(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		node, err := treeStore.GetBySlug(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := forum.MarkRead(r.Context(), deps.DB, user.ID, node.ID); err != nil {
			deps.Logger.Error("forum mark read", "err", err)
			http.Error(w, "Erreur marquage lu", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// hasCookie indique si la requête porte un cookie nommé (observabilité : distinguer
// un POST sans session d'un POST avec session non reconnue).
func hasCookie(r *http.Request, name string) bool {
	_, err := r.Cookie(name)
	return err == nil
}

// userID retourne l'id de l'utilisateur ou "" si anonyme.
func userID(u *identity.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}

// forumAttachMimes : types de pièces jointes autorisés (sniffés au contenu).
// Volontairement restreint à des formats sûrs à servir same-origin (pas de SVG/HTML).
var forumAttachMimes = map[string]string{
	"image/png":       ".png",
	"image/jpeg":      ".jpg",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"application/pdf": ".pdf",
}

func brandingUploadDir(deps app.AppDeps) string {
	if d := deps.Config.BrandingUploadDir; d != "" {
		return d
	}
	return "./uploads"
}

// handleForumPieceDownload sert une pièce jointe forum via route gardée. Vérifie
// la visibilité du nœud porteur (forumCanRead) avant d'émettre l'octet ; journalise
// l'accès légitime via gdpr.LogAccess (motif afppieces transposé au socle).
func handleForumPieceDownload(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		filename := chi.URLParam(r, "filename")
		if !forumPieceFilenameRe.MatchString(filename) {
			http.NotFound(w, r)
			return
		}
		user := middleware.UserFromContext(r.Context())
		carrierSlug := chi.URLParam(r, "slug")

		var carriers []*tree.Node
		if carrierSlug != "" {
			node, err := treeStore.GetBySlug(r.Context(), carrierSlug)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if !nodeReferencesForumPiece(node, filename) {
				http.NotFound(w, r)
				return
			}
			carriers = []*tree.Node{node}
		} else {
			var err error
			carriers, err = findForumPieceCarriers(r.Context(), deps.DB, filename)
			if err != nil {
				deps.Logger.Error("forum piece carriers", "filename", filename, "err", err)
				http.Error(w, "Erreur lecture pièce", http.StatusInternalServerError)
				return
			}
			if len(carriers) == 0 {
				http.NotFound(w, r)
				return
			}
		}

		var readable *tree.Node
		for _, c := range carriers {
			if forumCanRead(r.Context(), deps, user, c.ID) {
				readable = c
				break
			}
		}
		if readable == nil {
			http.NotFound(w, r)
			return
		}

		path := filepath.Join(brandingUploadDir(deps), "uploads", "forum", filename)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			deps.Logger.Error("forum piece open", "filename", filename, "err", err)
			http.Error(w, "Erreur lecture pièce", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		head := make([]byte, 512)
		n, _ := f.Read(head)
		mime := http.DetectContentType(head[:n])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "Erreur lecture pièce", http.StatusInternalServerError)
			return
		}

		actorID := "anonymous"
		if user != nil && user.ID != "" {
			actorID = user.ID
		}
		gdpr.LogAccess(r.Context(), &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
			SubjectKind: "forum_attachment",
			SubjectID:   readable.ID,
			ActorID:     actorID,
			Action:      gdpr.ActionExport,
			UserID:      userID(user),
		})

		w.Header().Set("Content-Type", mime)
		if !strings.HasPrefix(mime, "image/") {
			w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		}
		http.ServeContent(w, r, filename, readable.UpdatedAt, f)
	}
}

func nodeReferencesForumPiece(node *tree.Node, filename string) bool {
	return strings.Contains(node.BodyMD, filename) ||
		strings.Contains(node.BodyHTML, filename)
}

func findForumPieceCarriers(ctx context.Context, db *sql.DB, filename string) ([]*tree.Node, error) {
	store := &tree.Store{DB: db}
	likeStatic := "%/static/uploads/forum/" + filename + "%"
	likeGuarded := "%/forum/piece/%/" + filename + "%"
	likeGuardedAlt := "%/forum/piece/" + filename + "%"
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM nodes
		WHERE deleted_at IS NULL
		  AND (body_md LIKE ? OR body_md LIKE ? OR body_md LIKE ?
		       OR body_html LIKE ? OR body_html LIKE ? OR body_html LIKE ?)
	`, likeStatic, likeGuarded, likeGuardedAlt, likeStatic, likeGuarded, likeGuardedAlt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*tree.Node
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		n, err := store.GetByID(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// saveForumAttachments enregistre les pièces jointes ("attachments") d'un message
// forum sous uploads/forum/ et retourne le markdown à ajouter au corps (images
// inline, autres en lien), plus un message d'erreur non vide en cas de refus. Le
// type est sniffé au contenu (allowlist) et le nom de fichier généré côté serveur.
// carrierSlug est le slug du nœud porteur (message) pour la route gardée.
func saveForumAttachments(r *http.Request, deps app.AppDeps, carrierSlug string) (string, string) {
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return "", ""
	}
	headers := r.MultipartForm.File["attachments"]
	if len(headers) == 0 {
		return "", ""
	}
	dir := filepath.Join(brandingUploadDir(deps), "uploads", "forum")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", "Erreur de stockage des pièces jointes."
	}
	var md strings.Builder
	for _, h := range headers {
		f, err := h.Open()
		if err != nil {
			return "", "Pièce jointe illisible."
		}
		head := make([]byte, 512)
		n, _ := f.Read(head)
		mime := http.DetectContentType(head[:n])
		if i := strings.Index(mime, ";"); i > 0 {
			mime = strings.TrimSpace(mime[:i])
		}
		ext, ok := forumAttachMimes[mime]
		if !ok {
			f.Close()
			return "", "Type de pièce jointe non autorisé (images ou PDF uniquement)."
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return "", "Pièce jointe illisible."
		}
		content, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return "", "Pièce jointe illisible."
		}
		sum := sha256.Sum256(content)
		name := fmt.Sprintf("%x-%s", sum[:4], sanitizeForumFilename(h.Filename, ext))
		if err := os.WriteFile(filepath.Join(dir, name), content, 0640); err != nil {
			return "", "Erreur d'écriture de la pièce jointe."
		}
		url := forumPieceURL(carrierSlug, name)
		if strings.HasPrefix(mime, "image/") {
			fmt.Fprintf(&md, "![%s](%s)\n\n", name, url)
		} else {
			fmt.Fprintf(&md, "[\U0001F4CE %s](%s)\n\n", name, url)
		}
		if deps.Logger != nil {
			deps.Logger.Info("forum_attachment_saved", "name", name, "mime", mime, "size", len(content))
		}
	}
	return strings.TrimSpace(md.String()), ""
}

// sanitizeForumFilename rend un nom de fichier sûr ; un nom absent (capture collée)
// est remplacé par un nom déduit de l'extension du type.
func sanitizeForumFilename(name, ext string) string {
	name = filepath.Base(name)
	if name == "" || name == "." || name == "/" || name == "\\" {
		return "capture" + ext
	}
	var sb strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' {
			sb.WriteRune(c)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" {
		return "fichier" + ext
	}
	return s
}

// messageTitle dérive un titre court depuis le corps d'un message (le titre du
// nœud n'est pas affiché dans le fil chat, mais reste requis par le schéma).
func messageTitle(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\n", " "))
	r := []rune(body)
	if len(r) > 60 {
		return strings.TrimSpace(string(r[:60])) + "…"
	}
	return body
}

// handleForumNewQuestionForm rend le formulaire de création d'une question (enfant
// d'une catégorie), injecté dans le panneau de détail par HTMX.
func handleForumNewQuestionForm(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		cat, err := treeStore.GetBySlug(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		views.ForumNewQuestionForm(cat.Slug, cat.Title, middleware.CSRFToken(r.Context())).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleForumCreateQuestion crée une question (enfant d'une catégorie), puis re-rend
// le panneau des questions de cette catégorie (la nouvelle y apparaît). Garde
// d'écriture portée par RequirePerm sur la route.
func handleForumCreateQuestion(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		slug := chi.URLParam(r, "slug")
		cat, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" || len([]rune(title)) > 180 {
			deps.Logger.Warn("forum_question_rejected", "reason", "titre", "user_id", userID(user), "slug", slug)
			http.Error(w, "Titre obligatoire (max 180 caractères).", http.StatusBadRequest)
			return
		}
		node := tree.Node{
			ParentID: sql.NullString{String: cat.ID, Valid: true},
			Type:     "post",
			Title:    title,
			BodyMD:   body,
			BodyHTML: renderForumMD(body),
			AuthorID: sql.NullString{String: userID(user), Valid: user != nil},
		}
		newID, err := treeStore.Create(r.Context(), node)
		if err == tree.ErrSlugTaken {
			// UUIDv7 COMPLET : un préfixe tronqué (horodaté) collisionne même-milliseconde.
			node.Slug = "question-" + uid.New()
			newID, err = treeStore.Create(r.Context(), node)
		}
		if err != nil {
			deps.Logger.Error("forum_question_rejected", "reason", "create_error", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur création de la question.", http.StatusInternalServerError)
			return
		}
		deps.Logger.Info("forum_question_created", "user_id", userID(user), "question_id", newID, "category_slug", slug, "title_len", len(title))
		// Branche "Général" créée d'office, titrée comme la question (modèle pur :
		// les messages vivent sous une branche, pas directement sous la question).
		if err := createDefaultBranch(r.Context(), treeStore, newID, title, userID(user)); err != nil {
			deps.Logger.Error("forum_default_branch", "reason", "create_error", "question_id", newID, "err", err.Error())
		}

		category, err := forum.BuildThread(r.Context(), treeStore, *cat, authorOf, 0)
		if err != nil {
			http.Error(w, "Erreur lecture catégorie", http.StatusInternalServerError)
			return
		}
		questions, err := childThreads(r.Context(), treeStore, cat.ID, authorOf, 1)
		if err != nil {
			http.Error(w, "Erreur lecture questions", http.StatusInternalServerError)
			return
		}
		qIDs := make([]string, 0, len(questions))
		for _, q := range questions {
			qIDs = append(qIDs, q.ID)
		}
		unread, _ := forum.UnreadQuestions(r.Context(), deps.DB, userID(user), qIDs)
		views.ForumQuestionsColumn(category, questions, unread, forumCanWrite(r.Context(), deps, user)).Render(r.Context(), w) //nolint:errcheck
	}
}

// createDefaultBranch crée une branche enfant d'une question. Le slug est DÉRIVÉ
// (branch-<uid>) pour ne JAMAIS entrer en collision avec le slug de la question
// (qui est slugify(titre) — or la branche par défaut porte le même titre).
func createDefaultBranch(ctx context.Context, store *tree.Store, questionID, title, authorID string) error {
	_, err := store.Create(ctx, tree.Node{
		Slug:     "branch-" + uid.New(),
		ParentID: sql.NullString{String: questionID, Valid: true},
		Type:     "post",
		Title:    title,
		AuthorID: sql.NullString{String: authorID, Valid: authorID != ""},
	})
	return err
}

// handleForumBranches rend le panneau des branches d'une question (clone du panneau
// Questions, un cran plus bas). Fragment HTMX injecté dans #forum-col-channels.
func handleForumBranches(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		writeForumBranches(deps, treeStore, authorOf, w, r, chi.URLParam(r, "slug"))
	}
}

// writeForumBranches compose et écrit le fragment de la colonne des branches
// d'une question (slug). Partagé entre l'affichage du panneau et le re-rendu
// HTMX après création/renommage/suppression d'une branche.
func writeForumBranches(deps app.AppDeps, treeStore *tree.Store, authorOf func(ctx context.Context, userID string) string, w http.ResponseWriter, r *http.Request, slug string) {
	node, err := treeStore.GetBySlug(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	question, err := forum.BuildThread(r.Context(), treeStore, *node, authorOf, 0)
	if err != nil {
		http.Error(w, "Erreur lecture question", http.StatusInternalServerError)
		return
	}
	branches, err := childThreads(r.Context(), treeStore, node.ID, authorOf, 1)
	if err != nil {
		http.Error(w, "Erreur lecture branches", http.StatusInternalServerError)
		return
	}
	bIDs := make([]string, 0, len(branches))
	for _, b := range branches {
		bIDs = append(bIDs, b.ID)
	}
	u := middleware.UserFromContext(r.Context())
	// Une branche "a du nouveau" = un message (enfant DIRECT) plus récent que sa lecture.
	unread, _ := forum.UnreadSet(r.Context(), deps.DB, userID(u), bIDs)
	views.ForumBranchesColumn(question, branches, unread, forumCanWrite(r.Context(), deps, u)).Render(r.Context(), w) //nolint:errcheck
}

// handleForumNewBranchForm rend le formulaire de création d'une branche, injecté
// dans le panneau de détail.
func handleForumNewBranchForm(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := treeStore.GetBySlug(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		views.ForumNewBranchForm(q.Slug, q.Title, middleware.CSRFToken(r.Context())).Render(r.Context(), w) //nolint:errcheck
	}
}

// handleForumCreateBranch crée une branche (enfant d'une question) puis re-rend le
// panneau des branches. Garde d'écriture portée par RequirePerm sur la route.
func handleForumCreateBranch(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		user := middleware.UserFromContext(r.Context())
		slug := chi.URLParam(r, "slug")
		q, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" || len([]rune(title)) > 180 {
			deps.Logger.Warn("forum_branch_rejected", "reason", "titre", "user_id", userID(user), "slug", slug)
			http.Error(w, "Titre obligatoire (max 180 caractères).", http.StatusBadRequest)
			return
		}
		newID, err := treeStore.Create(r.Context(), tree.Node{
			Slug:     "branch-" + uid.New(),
			ParentID: sql.NullString{String: q.ID, Valid: true},
			Type:     "post",
			Title:    title,
			BodyMD:   body,
			BodyHTML: renderForumMD(body),
			AuthorID: sql.NullString{String: userID(user), Valid: user != nil},
		})
		if err != nil {
			deps.Logger.Error("forum_branch_rejected", "reason", "create_error", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur création de la branche.", http.StatusInternalServerError)
			return
		}
		deps.Logger.Info("forum_branch_created", "user_id", userID(user), "branch_id", newID, "question_slug", slug, "title_len", len(title))

		question, err := forum.BuildThread(r.Context(), treeStore, *q, authorOf, 0)
		if err != nil {
			http.Error(w, "Erreur lecture question", http.StatusInternalServerError)
			return
		}
		branches, err := childThreads(r.Context(), treeStore, q.ID, authorOf, 1)
		if err != nil {
			http.Error(w, "Erreur lecture branches", http.StatusInternalServerError)
			return
		}
		bIDs := make([]string, 0, len(branches))
		for _, b := range branches {
			bIDs = append(bIDs, b.ID)
		}
		unread, _ := forum.UnreadSet(r.Context(), deps.DB, userID(user), bIDs)
		views.ForumBranchesColumn(question, branches, unread, forumCanWrite(r.Context(), deps, user)).Render(r.Context(), w) //nolint:errcheck
	}
}

func handleForumReply(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		user := middleware.UserFromContext(r.Context())
		if user == nil {
			deps.Logger.Warn("forum_reply_rejected", "reason", "unauthenticated", "slug", slug, "hx_request", r.Header.Get("HX-Request"), "has_session_cookie", hasCookie(r, "assokit_session"))
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		parent, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			deps.Logger.Warn("forum_reply_rejected", "reason", "question_introuvable", "user_id", user.ID, "slug", slug)
			http.NotFound(w, r)
			return
		}
		deps.Logger.Info("forum_reply_attempt", "user_id", user.ID, "slug", slug, "parent_depth", parent.Depth,
			"content_type", r.Header.Get("Content-Type"), "hx_request", r.Header.Get("HX-Request"))
		if parent.Depth >= ForumMaxDepth-1 {
			deps.Logger.Warn("forum_reply_rejected", "reason", "max_depth", "user_id", user.ID, "slug", slug, "parent_depth", parent.Depth, "max", ForumMaxDepth)
			http.Error(w, "Profondeur de fil maximale atteinte.", http.StatusBadRequest)
			return
		}
		// Multipart (texte + pièces jointes). Tolère le non-multipart (la page de
		// repli poste en urlencodé) : ParseForm reste appelé en interne, donc
		// FormValue fonctionne dans les deux cas.
		const maxForumUpload = 25 << 20 // 25 MiB (texte + pièces jointes)
		r.Body = http.MaxBytesReader(w, r.Body, maxForumUpload+4096)
		_ = r.ParseMultipartForm(maxForumUpload)

		// Mode chat/SAV : un message OU une pièce jointe suffit. Le titre du nœud
		// (non affiché dans le fil) est dérivé du message ou d'un libellé par défaut.
		body := strings.TrimSpace(r.FormValue("body"))
		// Slug de la réponse pré-généré : les liens de pièces jointes pointent la
		// route gardée /forum/piece/{slug}/{filename} dès la création du message.
		replySlug := "reply-" + uid.New()
		attachMD, attachErr := saveForumAttachments(r, deps, replySlug)
		if attachErr != "" {
			deps.Logger.Warn("forum_reply_rejected", "reason", "attachment", "user_id", user.ID, "slug", slug, "detail", attachErr)
			middleware.PushFlash(w, "error", attachErr)
			http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
			return
		}
		if body == "" && attachMD == "" {
			deps.Logger.Warn("forum_reply_rejected", "reason", "corps_vide", "user_id", user.ID, "slug", slug,
				"raw_body_len", len(r.FormValue("body")), "multipart", r.MultipartForm != nil)
			middleware.PushFlash(w, "error", "Message ou pièce jointe obligatoire.")
			http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
			return
		}
		bodyMD := body
		if attachMD != "" {
			bodyMD = strings.TrimSpace(body + "\n\n" + attachMD)
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			if title = messageTitle(body); title == "" {
				title = "Pièce jointe"
			}
		}
		_, err = treeStore.Create(r.Context(), tree.Node{
			Slug:     replySlug,
			ParentID: sql.NullString{String: parent.ID, Valid: true},
			Type:     "post",
			Title:    title,
			BodyMD:   bodyMD,
			BodyHTML: renderForumMD(bodyMD),
			AuthorID: sql.NullString{String: user.ID, Valid: true},
		})
		if err != nil {
			deps.Logger.Error("forum_reply_rejected", "reason", "create_error", "user_id", user.ID, "slug", slug, "err", err.Error())
			middleware.PushFlash(w, "error", "Erreur création réponse.")
			http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
			return
		}
		deps.Logger.Info("forum_reply_created", "user_id", user.ID, "reply_slug", replySlug, "parent_slug", slug,
			"parent_depth", parent.Depth, "body_len", len(bodyMD), "hx_request", r.Header.Get("HX-Request"))
		if deps.EventSink != nil {
			_ = deps.EventSink.Emit(r.Context(), eventsink.Event{
				Type: "forum.post.created",
				Payload: map[string]any{
					"slug": replySlug, "parent_slug": slug, "title": title, "author_id": user.ID,
				},
			})
		}
		// Réponse postée depuis la station multi-panneaux : re-rendre le panneau
		// de détail (la nouvelle réponse y apparaît) au lieu de quitter la page.
		if r.Header.Get("HX-Request") == "true" {
			writeForumDetail(deps, treeStore, authorOf, w, r, slug)
			return
		}
		http.Redirect(w, r, "/forum/"+slug, http.StatusSeeOther)
	}
}

// handleForumRename renomme un nœud forum générique (catégorie, question ou
// branche — un nœud est un nœud). Met à jour le titre via tree.Update ; le slug
// reste stable (Update ne touche pas au slug). Re-rend ensuite le panneau parent
// d'où l'item est listé (swap HTMX). Garde d'écriture portée par RequirePerm sur
// la route.
func handleForumRename(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		node, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" || len([]rune(title)) > 180 {
			deps.Logger.Warn("forum_rename_rejected", "reason", "titre", "slug", slug)
			http.Error(w, "Titre obligatoire (max 180 caractères).", http.StatusBadRequest)
			return
		}
		node.Title = title
		if err := treeStore.Update(r.Context(), *node); err != nil {
			deps.Logger.Error("forum_rename_rejected", "reason", "update_error", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur renommage du nœud.", http.StatusInternalServerError)
			return
		}
		deps.Logger.Info("forum_node_renamed", "node_id", node.ID, "slug", slug, "title_len", len(title))
		writeForumParentPanel(deps, treeStore, authorOf, w, r, node)
	}
}

// handleForumDelete supprime un nœud forum générique en CASCADE (soft-delete :
// le nœud et tous ses descendants reçoivent deleted_at, aucune ligne n'est
// physiquement effacée — cf. tree.Delete). Re-rend ensuite le panneau parent (la
// colonne d'où l'item disparaît). Garde d'écriture portée par RequirePerm.
func handleForumDelete(deps app.AppDeps) http.HandlerFunc {
	treeStore := &tree.Store{DB: deps.DB}
	authorOf := authorResolver(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		node, err := treeStore.GetBySlug(r.Context(), slug)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := treeStore.Delete(r.Context(), node.ID); err != nil {
			deps.Logger.Error("forum_delete_rejected", "reason", "delete_error", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur suppression du nœud.", http.StatusInternalServerError)
			return
		}
		deps.Logger.Info("forum_node_deleted", "node_id", node.ID, "slug", slug)
		// node porte encore son ParentID (lu avant suppression) → on peut re-rendre
		// la colonne parente d'où l'item vient de disparaître.
		writeForumParentPanel(deps, treeStore, authorOf, w, r, node)
	}
}

// writeForumParentPanel re-rend la colonne HTMX parente du nœud passé, en
// réutilisant les fragments de panneau existants. Le niveau du parent décide
// du fragment : une question vit dans la colonne des questions de sa catégorie,
// une branche dans la colonne des branches de sa question. Pour une catégorie
// (parent = racine forum), aucune colonne-fragment dédiée n'existe : on demande
// au client un rechargement complet de /forum via l'en-tête HX-Redirect, qui
// rafraîchit le panneau des catégories rendu par la page d'index.
func writeForumParentPanel(deps app.AppDeps, treeStore *tree.Store, authorOf func(ctx context.Context, userID string) string, w http.ResponseWriter, r *http.Request, node *tree.Node) {
	if !node.ParentID.Valid || node.ParentID.String == "" {
		w.Header().Set("HX-Redirect", "/forum")
		w.WriteHeader(http.StatusOK)
		return
	}
	parent, err := treeStore.GetByID(r.Context(), node.ParentID.String)
	if err != nil {
		// Parent introuvable (ex. racine non chargeable) → rechargement complet.
		w.Header().Set("HX-Redirect", "/forum")
		w.WriteHeader(http.StatusOK)
		return
	}
	switch parent.Depth {
	case 0:
		// node est une catégorie (parent = racine forum) : pas de fragment dédié.
		w.Header().Set("HX-Redirect", "/forum")
		w.WriteHeader(http.StatusOK)
	case 1:
		// node est une question (parent = catégorie) : colonne des questions.
		writeForumQuestions(deps, treeStore, authorOf, w, r, parent.Slug)
	case 2:
		// node est une branche (parent = question) : colonne des branches.
		writeForumBranches(deps, treeStore, authorOf, w, r, parent.Slug)
	default:
		// node est un message (parent = branche, profondeur >= 3) : panneau détail.
		// La branche joue le rôle de "question" affichée dans ce panneau (cf.
		// writeForumDetail — le slug reçu y est en pratique celui d'une branche).
		writeForumDetail(deps, treeStore, authorOf, w, r, parent.Slug)
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
	store := &identity.Store{DB: db}
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
