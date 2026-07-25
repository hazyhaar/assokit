// CLAUDE:SUMMARY Tests sécurité W1.5 : pièces jointes forum route gardée (404 statique,
// accès conditionnel, journal gdpr) + /search respecte forumCanRead sur fils restreints.
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/bootstrap"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
	tree "github.com/hazyhaar/nodetree"
)

// png1x1 est un PNG 1×1 réel (octets valides) pour le flux d'upload forum.
var png1x1 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xde, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d,
	0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

func forumSecurityDeps(t *testing.T) (app.AppDeps, *tree.Store, string) {
	t.Helper()
	db := newTestDB(t)
	if err := bootstrap.BootstrapForumPerms(db); err != nil {
		t.Fatalf("BootstrapForumPerms: %v", err)
	}
	for _, role := range []string{"public", "reader"} {
		if _, err := db.Exec(`INSERT INTO roles(id, label) VALUES(?,?) ON CONFLICT DO NOTHING`, role, role); err != nil {
			t.Fatalf("seed role %s: %v", role, err)
		}
	}
	uploadDir := t.TempDir()
	deps := app.AppDeps{
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: config.Config{BrandingUploadDir: uploadDir},
	}
	seedForumSecurityUsers(t, db)
	return deps, &tree.Store{DB: db}, uploadDir
}

// seedForumSecurityUsers insère les utilisateurs référencés par author_id lors de
// handleForumReply (FK nodes.author_id → users.id).
func seedForumSecurityUsers(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, u := range []struct{ id, email string }{
		{"u-member", "m@test.com"},
		{"u-reader", "r@test.com"},
	} {
		if _, err := db.Exec(
			`INSERT INTO users(id, email, password_hash, display_name) VALUES(?,?,?,?)
			 ON CONFLICT(id) DO NOTHING`,
			u.id, u.email, "x", u.id,
		); err != nil {
			t.Fatalf("seed user %s: %v", u.id, err)
		}
	}
}

func seedForumTree(t *testing.T, store *tree.Store, ps *perms.Store, restricted bool) (catSlug, questionSlug string) {
	t.Helper()
	ctx := context.Background()
	forumRoot, err := store.Create(ctx, tree.Node{Slug: "forum", Type: "folder", Title: "Forum"})
	if err != nil && err != tree.ErrSlugTaken {
		t.Fatalf("forum root: %v", err)
	}
	if err == tree.ErrSlugTaken {
		n, _ := store.GetBySlug(ctx, "forum")
		forumRoot = n.ID
	}
	catID, err := store.Create(ctx, tree.Node{
		Slug: "cat-secreta", Type: "post", Title: "Catégorie restreinte",
		ParentID: sql.NullString{String: forumRoot, Valid: true},
	})
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	cat, _ := store.GetByID(ctx, catID)
	catSlug = cat.Slug
	if restricted {
		if err := ps.Set(ctx, catID, "member", perms.PermRead); err != nil {
			t.Fatalf("perm member read: %v", err)
		}
	} else {
		if err := ps.Set(ctx, catID, "public", perms.PermRead); err != nil {
			t.Fatalf("perm public read: %v", err)
		}
	}
	qID, err := store.Create(ctx, tree.Node{
		Slug: "q-secrete", Type: "post", Title: "Question confidentielle zephyr42",
		ParentID: sql.NullString{String: catID, Valid: true},
	})
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	q, _ := store.GetByID(ctx, qID)
	return catSlug, q.Slug
}

func postForumReplyWithPNG(t *testing.T, deps app.AppDeps, questionSlug string, user *identity.User) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("body", "voir pièce")
	part, err := w.CreateFormFile("attachments", "preuve.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(png1x1); err != nil {
		t.Fatalf("write png: %v", err)
	}
	_ = w.Close()

	guarded := middleware.RequirePerm(deps.DB, perms.PermWrite, func(*http.Request) string {
		return "node-forum"
	})(handleForumReply(deps))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", questionSlug)
	r := httptest.NewRequest(http.MethodPost, "/forum/"+questionSlug+"/reply", &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())
	if user != nil {
		r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	}
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reply code = %d, want 303 body=%s", rec.Code, rec.Body.String())
	}

	rows, err := deps.DB.Query(`SELECT body_md FROM nodes WHERE body_md LIKE '%/forum/piece/%' ORDER BY created_at DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("query reply: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("aucune réponse avec pièce jointe (redirect=%q flash=%q)",
			rec.Header().Get("Location"), forumTestFlash(rec))
	}
	var bodyMD string
	if err := rows.Scan(&bodyMD); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Extrait le nom de fichier depuis le markdown généré.
	const marker = "/forum/piece/"
	i := strings.LastIndex(bodyMD, marker)
	if i < 0 {
		t.Fatalf("markdown sans route gardée: %q", bodyMD)
	}
	rest := bodyMD[i+len(marker):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("markdown mal formé: %q", bodyMD)
	}
	filename := parts[1]
	if j := strings.IndexAny(filename, ")\n \t"); j >= 0 {
		filename = filename[:j]
	}
	if !forumPieceFilenameRe.MatchString(filename) {
		t.Fatalf("filename extrait invalide %q depuis %q", filename, bodyMD)
	}
	return filename
}

func forumTestFlash(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name != "assokit_flash" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(c.Value)
		if err != nil {
			return c.Value
		}
		var flashes []struct {
			Level   string `json:"Level"`
			Message string `json:"Message"`
		}
		if err := json.Unmarshal(raw, &flashes); err != nil {
			return string(raw)
		}
		var msgs []string
		for _, f := range flashes {
			msgs = append(msgs, f.Message)
		}
		return strings.Join(msgs, "; ")
	}
	return ""
}

func forumSecurityRouter(deps app.AppDeps) chi.Router {
	r := chi.NewRouter()
	r.Get("/forum/piece/{slug}/{filename}", handleForumPieceDownload(deps))
	r.Get("/forum/piece/{filename}", handleForumPieceDownload(deps))
	r.HandleFunc("/static/uploads/forum/*", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return r
}

func getForumPiece(r chi.Router, path string, user *identity.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != nil {
		req = req.WithContext(middleware.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestForumPiece_GuardedRouteSecurity(t *testing.T) {
	deps, store, uploadDir := forumSecurityDeps(t)
	ps := &perms.Store{DB: deps.DB}
	_, questionSlug := seedForumTree(t, store, ps, true)

	member := &identity.User{ID: "u-member", Email: "m@test.com", Roles: []string{"member"}}
	reader := &identity.User{ID: "u-reader", Email: "r@test.com", Roles: []string{"reader"}}

	filename := postForumReplyWithPNG(t, deps, questionSlug, member)
	if !forumPieceFilenameRe.MatchString(filename) {
		t.Fatalf("filename inattendu: %q", filename)
	}
	if _, err := os.Stat(filepath.Join(uploadDir, "uploads", "forum", filename)); err != nil {
		t.Fatalf("fichier disque absent: %v", err)
	}

	// Récupère le slug porteur (réponse) pour la route gardée.
	var replySlug string
	err := deps.DB.QueryRow(`SELECT slug FROM nodes WHERE body_md LIKE ? ORDER BY created_at DESC LIMIT 1`,
		"%"+filename+"%").Scan(&replySlug)
	if err != nil {
		t.Fatalf("reply slug: %v", err)
	}
	guardedPath := forumPieceURL(replySlug, filename)
	staticPath := "/static/uploads/forum/" + filename

	r := forumSecurityRouter(deps)

	// Ancienne URL statique → 404.
	if w := getForumPiece(r, staticPath, member); w.Code != http.StatusNotFound {
		t.Errorf("static URL code = %d, want 404", w.Code)
	}

	// Lecteur légitime (member) → sert l'octet.
	w := getForumPiece(r, guardedPath, member)
	if w.Code != http.StatusOK {
		t.Errorf("guarded member code = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("Content-Type = %q, want image/*", ct)
	}

	// Utilisateur sans rôle sur la catégorie → refus (404 uniforme).
	if w := getForumPiece(r, guardedPath, reader); w.Code != http.StatusNotFound {
		t.Errorf("reader code = %d, want 404", w.Code)
	}

	// Anonyme → refus.
	if w := getForumPiece(r, guardedPath, nil); w.Code != http.StatusNotFound {
		t.Errorf("anonymous code = %d, want 404", w.Code)
	}
}

func TestForumPiece_AccessLogAfterDownload(t *testing.T) {
	deps, store, _ := forumSecurityDeps(t)
	ps := &perms.Store{DB: deps.DB}
	_, questionSlug := seedForumTree(t, store, ps, true)
	member := &identity.User{ID: "u-member", Email: "m@test.com", Roles: []string{"member"}}

	filename := postForumReplyWithPNG(t, deps, questionSlug, member)
	var replySlug string
	if err := deps.DB.QueryRow(`SELECT slug FROM nodes WHERE body_md LIKE ? ORDER BY created_at DESC LIMIT 1`,
		"%"+filename+"%").Scan(&replySlug); err != nil {
		t.Fatalf("reply slug: %v", err)
	}

	var before int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM data_access_log`).Scan(&before)

	r := forumSecurityRouter(deps)
	w := getForumPiece(r, forumPieceURL(replySlug, filename), member)
	if w.Code != http.StatusOK {
		t.Fatalf("download code = %d, want 200", w.Code)
	}

	var after int
	deps.DB.QueryRow(`SELECT COUNT(*) FROM data_access_log`).Scan(&after)
	if after != before+1 {
		t.Fatalf("journal : before=%d after=%d, attendu +1", before, after)
	}

	var action, actor, subjectKind string
	err := deps.DB.QueryRow(`
		SELECT action, actor_id, subject_kind FROM data_access_log
		ORDER BY created_at DESC LIMIT 1`).Scan(&action, &actor, &subjectKind)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if action != gdpr.ActionExport {
		t.Errorf("action = %q, want %q", action, gdpr.ActionExport)
	}
	if actor != member.ID {
		t.Errorf("actor = %q, want %q", actor, member.ID)
	}
	if subjectKind != "forum_attachment" {
		t.Errorf("subject_kind = %q, want forum_attachment", subjectKind)
	}
}

func TestSearch_ForumRestrictedVisibility(t *testing.T) {
	deps, store, _ := forumSecurityDeps(t)
	ps := &perms.Store{DB: deps.DB}
	seedForumTree(t, store, ps, true)

	member := &identity.User{ID: "u-member", Email: "m@test.com", Roles: []string{"member"}}
	reader := &identity.User{ID: "u-reader", Email: "r@test.com", Roles: []string{"reader"}}

	search := handleSearch(deps)

	doSearch := func(user *identity.User, q string) string {
		r := httptest.NewRequest(http.MethodGet, "/search?q="+q, nil)
		if user != nil {
			r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
		}
		w := httptest.NewRecorder()
		search(w, r)
		return w.Body.String()
	}

	term := "zephyr42"
	bodyReader := doSearch(reader, term)
	if strings.Contains(bodyReader, "Question confidentielle") {
		t.Error("lecteur sans rôle : le fil restreint ne doit pas apparaître dans /search")
	}

	bodyMember := doSearch(member, term)
	if !strings.Contains(bodyMember, "Question confidentielle") {
		t.Error("lecteur member : le fil restreint doit apparaître dans /search")
	}
}
