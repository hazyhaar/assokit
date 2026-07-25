package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/salon"
	"log/slog"
	"os"
)

func newTestDepsWithDB(t *testing.T) app.AppDeps {
	t.Helper()
	db := newTestDB(t)
	return app.AppDeps{
		DB:     db,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

func seedTestUser(t *testing.T, deps app.AppDeps) *identity.User {
	t.Helper()
	_, err := deps.DB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,1)`,
		"test-user-1", "test@example.com", "$2a$12$dummy", "Utilisateur Test",
	)
	if err != nil {
		t.Fatalf("seedTestUser: %v", err)
	}
	return &identity.User{ID: "test-user-1", Email: "test@example.com", DisplayName: "Utilisateur Test"}
}

// TestSalonList_EmptyState vérifie que GET /salons renvoie 200 avec l'état vide.
func TestSalonList_EmptyState(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	r := httptest.NewRequest(http.MethodGet, "/salons", nil)
	r = r.WithContext(middleware.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()

	handleSalonList(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /salons attendu 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Salons de discussion") {
		t.Errorf("titre attendu dans la réponse")
	}
}

// TestSalonCreateAndPage crée un salon, poste un message, vérifie le fragment.
func TestSalonCreateAndPage(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	// Créer un salon via le store directement (test unitaire du handler, pas du formulaire).
	store := &salon.Store{DB: deps.DB}
	sl, err := store.Create(context.Background(), "Salon Test", user.ID, "", false)
	if err != nil {
		t.Fatalf("Create salon: %v", err)
	}

	// Poster un message.
	_, err = store.PostMessage(context.Background(), sl.Slug, user.ID, "Bonjour tout le monde")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	// GET /salon/{slug} — page complète.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	r := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug, nil)
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), user),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()

	handleSalonPage(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /salon/%s attendu 200, got %d — body: %s", sl.Slug, w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Bonjour tout le monde") {
		t.Errorf("message attendu dans la page salon, body extrait: %q", body[:salonMin(200, len(body))])
	}

	// GET /salon/{slug}/messages/fragment — fragment de polling.
	r2 := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug+"/messages/fragment", nil)
	r2 = r2.WithContext(context.WithValue(
		middleware.ContextWithUser(r2.Context(), user),
		chi.RouteCtxKey, rctx,
	))
	w2 := httptest.NewRecorder()

	handleSalonMessagesFragment(deps).ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("GET fragment attendu 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "Bonjour tout le monde") {
		t.Errorf("message attendu dans le fragment HTMX")
	}
}

// TestSalonVisioButton_ApparaitSiVisioActive vérifie que le bouton "Rejoindre la visio"
// apparaît dans la page quand visio_enabled=true.
func TestSalonVisioButton_ApparaitSiVisioActive(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	store := &salon.Store{DB: deps.DB}
	sl, err := store.Create(context.Background(), "Salon Visio Test", user.ID, "", true)
	if err != nil {
		t.Fatalf("Create salon: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	r := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug, nil)
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), user),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()

	handleSalonPage(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Rejoindre la visio") {
		t.Errorf("bouton 'Rejoindre la visio' attendu quand visio_enabled=true")
	}
}

// TestSalonPage_NonMembre vérifie que GET /salon/{slug} renvoie 403 pour un non-membre.
func TestSalonPage_NonMembre(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	// Créer un second utilisateur non-membre.
	_, err := deps.DB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,1)`,
		"other-user", "other@example.com", "$2a$12$dummy", "Autre",
	)
	if err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	otherUser := &identity.User{ID: "other-user", Email: "other@example.com"}

	store := &salon.Store{DB: deps.DB}
	sl, err := store.Create(context.Background(), "Salon Privé", user.ID, "", false)
	if err != nil {
		t.Fatalf("Create salon: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	r := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug, nil)
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), otherUser),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()

	handleSalonPage(deps).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-membre : attendu 403, got %d", w.Code)
	}
}

// TestSalonPostMessage_AntiXSS vérifie qu'un message contenant <script> ne produit
// pas de balise script exécutable dans le HTML rendu (body_html généré par markup.Render).
func TestSalonPostMessage_AntiXSS(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	store := &salon.Store{DB: deps.DB}
	sl, err := store.Create(context.Background(), "Salon XSS Test", user.ID, "", false)
	if err != nil {
		t.Fatalf("Create salon: %v", err)
	}

	// Poster un message avec une charge XSS.
	payload := "<script>alert(document.cookie)</script>"
	_, err = store.PostMessage(context.Background(), sl.Slug, user.ID, payload)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	// Vérifier le body_html stocké directement : ne doit pas contenir <script.
	msgs, err := store.Messages(context.Background(), sl.Slug, 10)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("aucun message stocké")
	}
	if strings.Contains(msgs[0].BodyHTML, "<script") {
		t.Errorf("body_html stocké contient <script> : %q", msgs[0].BodyHTML)
	}

	// Vérifier le rendu HTML via le fragment handler.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	r := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug+"/messages/fragment", nil)
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), user),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()
	handleSalonMessagesFragment(deps).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("fragment: attendu 200, got %d", w.Code)
	}
	rendered := w.Body.String()
	if strings.Contains(rendered, "<script") {
		t.Errorf("HTML rendu contient <script> : devrait être échappé. Extrait: %q",
			rendered[:salonMin(300, len(rendered))])
	}
}

// TestSalonToggleVisio_NonOwnerRejeté vérifie que le POST toggle-visio d'un non-owner → 403.
func TestSalonToggleVisio_NonOwnerRejeté(t *testing.T) {
	deps := newTestDepsWithDB(t)
	owner := seedTestUser(t, deps)

	_, err := deps.DB.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, is_active) VALUES(?,?,?,?,1)`,
		"member-user", "member@example.com", "$2a$12$dummy", "Membre",
	)
	if err != nil {
		t.Fatalf("seed member user: %v", err)
	}
	member := &identity.User{ID: "member-user", Email: "member@example.com"}

	store := &salon.Store{DB: deps.DB}
	sl, err := store.Create(context.Background(), "Salon ToggleVisio", owner.ID, "", false)
	if err != nil {
		t.Fatalf("Create salon: %v", err)
	}
	if err := store.Join(context.Background(), sl.Slug, member.ID, ""); err != nil {
		t.Fatalf("Join: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	form := url.Values{"visio_enabled": {"1"}}
	r := httptest.NewRequest(http.MethodPost, "/salon/"+sl.Slug+"/toggle-visio",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), member),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()

	handleSalonToggleVisio(deps).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("toggle-visio non-owner : attendu 403, got %d", w.Code)
	}
}

func salonMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestCDN_Absence vérifie l'absence de référence CDN dans les vues salon.
func TestCDN_Absence(t *testing.T) {
	deps := newTestDepsWithDB(t)
	user := seedTestUser(t, deps)

	store := &salon.Store{DB: deps.DB}
	sl, _ := store.Create(context.Background(), "CDN Check", user.ID, "", false)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", sl.Slug)

	r := httptest.NewRequest(http.MethodGet, "/salon/"+sl.Slug, nil)
	r = r.WithContext(context.WithValue(
		middleware.ContextWithUser(r.Context(), user),
		chi.RouteCtxKey, rctx,
	))
	w := httptest.NewRecorder()
	handleSalonPage(deps).ServeHTTP(w, r)

	body := w.Body.String()
	for _, cdn := range []string{"cdn.jsdelivr.net", "unpkg.com", "cdnjs.cloudflare.com", "cdn.tailwindcss.com"} {
		if strings.Contains(body, cdn) {
			t.Errorf("référence CDN détectée dans le rendu : %s", cdn)
		}
	}
}

