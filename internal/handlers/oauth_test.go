// CLAUDE:SUMMARY Tests handlers OAuth : consent redirect, consent approval, Google callback mock (M-ASSOKIT-OAUTH-1).
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"log/slog"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/internal/config"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	_ "modernc.org/sqlite"
)

func newOAuthTestDeps(t *testing.T) (app.AppDeps, *oauth.Storage) {
	t.Helper()
	db := newTestDB(t)
	seedRoles(t, db)
	store := oauth.New(db, []byte("test-key-32bytes-abcdefghijklmno"), &rbac.Store{DB: db})
	return app.AppDeps{
		DB:     db,
		Logger: slog.Default(),
		Config: config.Config{BaseURL: "http://localhost:8080"},
	}, store
}

func seedOAuthUser(t *testing.T, deps app.AppDeps) string {
	t.Helper()
	authStore := &auth.Store{DB: deps.DB}
	u, err := authStore.Register(context.Background(), "oauth@test.fr", "pass123", "OAuth User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u.ID
}

// TestOAuthConsent_RedirectsIfNotLoggedIn : GET /oauth2/consent sans session → redirect /login.
func TestOAuthConsent_RedirectsIfNotLoggedIn(t *testing.T) {
	deps, store := newOAuthTestDeps(t)
	userID := seedOAuthUser(t, deps)
	_ = userID

	store.CreateClient(context.Background(), "cl-1", "sec-1",
		[]string{"http://localhost/cb"},
		[]string{string(oidc.GrantTypeCode)},
		[]string{"openid"},
	)

	ar, err := store.CreateAuthRequest(context.Background(), &oidc.AuthRequest{
		ClientID:     "cl-1",
		RedirectURI:  "http://localhost/cb",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid"},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth2/consent?id="+ar.GetID(), nil)
	w := httptest.NewRecorder()
	handleOAuthConsent(deps, store)(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("attendu 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Errorf("attendu redirect /login, got %s", loc)
	}
}

// TestOAuthConsent_ShowsPageIfLoggedIn : GET /oauth2/consent avec session → 200 avec page consent.
func TestOAuthConsent_ShowsPageIfLoggedIn(t *testing.T) {
	deps, store := newOAuthTestDeps(t)
	userID := seedOAuthUser(t, deps)

	store.CreateClient(context.Background(), "cl-2", "sec-2",
		[]string{"http://localhost/cb"},
		[]string{string(oidc.GrantTypeCode)},
		[]string{"openid"},
	)

	ar, err := store.CreateAuthRequest(context.Background(), &oidc.AuthRequest{
		ClientID:     "cl-2",
		RedirectURI:  "http://localhost/cb",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid", "profile"},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth2/consent?id="+ar.GetID(), nil)
	u := &auth.User{ID: userID, Roles: []string{"member"}}
	req = req.WithContext(middleware.ContextWithUser(req.Context(), u))

	w := httptest.NewRecorder()
	handleOAuthConsent(deps, store)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("attendu 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Demande d'autorisation") {
		t.Error("page consent: titre absent")
	}
}

// TestOAuthConsentPost_AllowRedirectsToCallback : POST consent allow → redirect /oauth2/authorize/callback.
func TestOAuthConsentPost_AllowRedirectsToCallback(t *testing.T) {
	deps, store := newOAuthTestDeps(t)
	userID := seedOAuthUser(t, deps)

	store.CreateClient(context.Background(), "cl-3", "sec-3",
		[]string{"http://localhost/cb"},
		[]string{string(oidc.GrantTypeCode)},
		[]string{"openid"},
	)

	ar, err := store.CreateAuthRequest(context.Background(), &oidc.AuthRequest{
		ClientID:     "cl-3",
		RedirectURI:  "http://localhost/cb",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid"},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}

	form := url.Values{"id": {ar.GetID()}, "decision": {"allow"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/consent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u := &auth.User{ID: userID, Roles: []string{"member"}}
	req = req.WithContext(middleware.ContextWithUser(req.Context(), u))

	w := httptest.NewRecorder()
	handleOAuthConsentPost(deps, store)(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("attendu 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/oauth2/authorize/callback") {
		t.Errorf("attendu redirect /oauth2/authorize/callback, got %s", loc)
	}
	if !strings.Contains(loc, ar.GetID()) {
		t.Errorf("location devrait contenir l'id de la requête: %s", loc)
	}
}

// TestOAuthConsentPost_DenyRedirectsWithError : POST consent deny → redirect avec error=access_denied.
func TestOAuthConsentPost_DenyRedirectsWithError(t *testing.T) {
	deps, store := newOAuthTestDeps(t)
	userID := seedOAuthUser(t, deps)

	store.CreateClient(context.Background(), "cl-4", "sec-4",
		[]string{"http://localhost/cb"},
		[]string{string(oidc.GrantTypeCode)},
		[]string{"openid"},
	)

	ar, err := store.CreateAuthRequest(context.Background(), &oidc.AuthRequest{
		ClientID:     "cl-4",
		RedirectURI:  "http://localhost/cb",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid"},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}

	form := url.Values{"id": {ar.GetID()}, "decision": {"deny"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/consent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u := &auth.User{ID: userID, Roles: []string{"member"}}
	req = req.WithContext(middleware.ContextWithUser(req.Context(), u))

	w := httptest.NewRecorder()
	handleOAuthConsentPost(deps, store)(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("attendu 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=access_denied") {
		t.Errorf("attendu error=access_denied, got %s", loc)
	}
}

// TestOAuth_GoogleLoginCallbackCreatesUserOrLinksExisting : teste findOrCreateSocialUser.
func TestOAuth_GoogleLoginCallbackCreatesUserOrLinksExisting(t *testing.T) {
	deps, _ := newOAuthTestDeps(t)
	ctx := context.Background()

	// Cas 1 : nouvel utilisateur (email inconnu)
	userID1, err := findOrCreateSocialUser(ctx, deps, "google", "google-sub-1", "newuser@google.com")
	if err != nil {
		t.Fatalf("findOrCreateSocialUser (nouveau): %v", err)
	}
	if userID1 == "" {
		t.Error("userID1 vide")
	}

	// Vérifier le lien oauth_external_links
	var linkUserID string
	err = deps.DB.QueryRowContext(ctx,
		`SELECT user_id FROM oauth_external_links WHERE provider = 'google' AND external_id = 'google-sub-1'`,
	).Scan(&linkUserID)
	if err != nil {
		t.Fatalf("oauth_external_links: %v", err)
	}
	if linkUserID != userID1 {
		t.Errorf("lien externe: want %s, got %s", userID1, linkUserID)
	}

	// Cas 2 : utilisateur existant (même email)
	userID2, err := findOrCreateSocialUser(ctx, deps, "google", "google-sub-1-bis", "newuser@google.com")
	if err != nil {
		t.Fatalf("findOrCreateSocialUser (existant): %v", err)
	}
	if userID2 != userID1 {
		t.Errorf("même email → même userID: want %s, got %s", userID1, userID2)
	}

	// Vérifier que le linked_at est présent
	var linkedAt string
	deps.DB.QueryRowContext(ctx,
		`SELECT linked_at FROM oauth_external_links WHERE provider = 'google' AND external_id = 'google-sub-1'`,
	).Scan(&linkedAt) //nolint:errcheck
	if linkedAt == "" {
		t.Error("oauth_external_links.linked_at vide")
	}
}
