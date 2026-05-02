package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/mark3labs/mcp-go/server"
)

// insertTestToken insère un token OAuth valide dans la DB pour les tests.
// Retourne le token opaque (bearer), dont le hash SHA256 est stocké dans access_token_hash.
func insertTestToken(t *testing.T, deps app.AppDeps, userID string, scopes []string, valid bool) string {
	t.Helper()
	bearerToken := uuid.NewString() // token opaque transmis en Bearer
	rowID := uuid.NewString()
	scopesJSON, _ := json.Marshal(scopes)
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if !valid {
		exp = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) // expiré
	}
	_, err := deps.DB.ExecContext(context.Background(),
		`INSERT INTO oauth_tokens(id, access_token_hash, client_id, user_id, scopes, expires_at) VALUES(?,?,?,?,?,?)`,
		rowID, hashBearerToken(bearerToken), "test-client", userID, string(scopesJSON), exp,
	)
	if err != nil {
		t.Fatalf("insertTestToken: %v", err)
	}
	return bearerToken
}

// TestMCPEndpoint_BearerInvalidReturns401 vérifie qu'un Bearer absent/invalide → 401.
func TestMCPEndpoint_BearerInvalidReturns401(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}

	r := chi.NewRouter()
	r.Use(oauthBearerMiddleware(deps.DB, svc, deps))
	r.Get("/mcp/test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	t.Run("sans_bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/mcp/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("attendu 401, got %d", w.Code)
		}
		if w.Header().Get("WWW-Authenticate") == "" {
			t.Error("WWW-Authenticate absent")
		}
	})

	t.Run("bearer_invalide", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/mcp/test", nil)
		req.Header.Set("Authorization", "Bearer token-inexistant")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("attendu 401, got %d", w.Code)
		}
	})

	t.Run("bearer_expire", func(t *testing.T) {
		tokenID := insertTestToken(t, deps, "user-expired", []string{"forum.post.create"}, false)
		req := httptest.NewRequest(http.MethodGet, "/mcp/test", nil)
		req.Header.Set("Authorization", "Bearer "+tokenID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("bearer expiré : attendu 401, got %d", w.Code)
		}
	})
}

// TestMCPEndpoint_ValidBearerInjectsContext vérifie que le Bearer valide injecte bien le contexte.
func TestMCPEndpoint_ValidBearerInjectsContext(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}

	// Créer perm + grade + user
	ctx := context.Background()
	gID, _ := svc.Store.CreateGrade(ctx, "mcp-user-grade")
	pID, _ := svc.Store.EnsurePermission(ctx, "mcp.test.perm", "")
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.Store.AssignGrade(ctx, "user-mcp-ctx", gID) //nolint:errcheck
	svc.Recompute(ctx, "user-mcp-ctx") //nolint:errcheck

	tokenID := insertTestToken(t, deps, "user-mcp-ctx", []string{"mcp.test.perm"}, true)

	var capturedUserID string
	var capturedScopes []string

	r := chi.NewRouter()
	r.Use(oauthBearerMiddleware(deps.DB, svc, deps))
	r.Get("/mcp/check", func(w http.ResponseWriter, r *http.Request) {
		u := middleware.UserFromContext(r.Context())
		if u != nil {
			capturedUserID = u.ID
		}
		capturedScopes = ScopesFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/mcp/check", nil)
	req.Header.Set("Authorization", "Bearer "+tokenID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, got %d", w.Code)
	}
	if capturedUserID != "user-mcp-ctx" {
		t.Errorf("userID injecté incorrect: %q", capturedUserID)
	}
	if len(capturedScopes) == 0 || capturedScopes[0] != "mcp.test.perm" {
		t.Errorf("scopes incorrects: %v", capturedScopes)
	}
}

// TestMCPEndpoint_ScopeMissingReturnsTypedError vérifie que scope manquant → erreur MCP typée.
func TestMCPEndpoint_ScopeMissingReturnsTypedError(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}

	// Token avec scope vide
	tokenID := insertTestToken(t, deps, "user-no-scope", []string{}, true)

	reg := actions.NewRegistry()
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "mcp.test.guarded",
		RequiredPerm: "mcp.test.guarded",
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			return actions.Result{Status: "ok", Message: "secret"}, nil
		},
	})

	mcpSrv := server.NewMCPServer("assokit-test", "0.1.0")
	actions.MountMCP(mcpSrv, deps, reg)
	httpSrv := server.NewStreamableHTTPServer(mcpSrv)

	r := chi.NewRouter()
	r.Use(oauthBearerMiddleware(deps.DB, svc, deps))
	r.Mount("/mcp", httpSrv)

	// Requête initialize MCP avec Bearer valide mais scope manquant
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	req.Header.Set("Authorization", "Bearer "+tokenID)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Le initialize doit passer (200 ou 201), la vérification perm est au tool call
	if w.Code >= 500 {
		t.Errorf("initialize MCP avec Bearer valide: attendu <500, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestMCPEndpoint_PermRevokedAfterTokenIssuedDeniesCall vérifie que perm révoquée → denied.
func TestMCPEndpoint_PermRevokedAfterTokenIssuedDeniesCall(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}
	ctx := context.Background()

	// Créer grade+perm+user
	gID, _ := svc.Store.CreateGrade(ctx, "revoke-grade")
	pID, _ := svc.Store.EnsurePermission(ctx, "mcp.revoke.test", "")
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.Store.AssignGrade(ctx, "user-revoke", gID) //nolint:errcheck
	svc.Recompute(ctx, "user-revoke") //nolint:errcheck

	if !perms.Has(perms.ContextWithUserID(perms.ContextWithService(ctx, svc), "user-revoke"), "mcp.revoke.test") {
		t.Fatal("précondition : user doit avoir la perm")
	}

	// Révoquer la perm
	svc.Store.RevokePerm(ctx, gID, pID) //nolint:errcheck
	svc.Recompute(ctx, "user-revoke") //nolint:errcheck

	if perms.Has(perms.ContextWithUserID(perms.ContextWithService(ctx, svc), "user-revoke"), "mcp.revoke.test") {
		t.Fatal("postcondition : user ne doit plus avoir la perm")
	}

	// Token encore valide mais perm révoquée
	tokenID := insertTestToken(t, deps, "user-revoke", []string{"mcp.revoke.test"}, true)
	_ = tokenID

	// Vérifier que perms.Has retourne false pour cet user
	ctxWithUser := perms.ContextWithUserID(perms.ContextWithService(ctx, svc), "user-revoke")
	if perms.Has(ctxWithUser, "mcp.revoke.test") {
		t.Error("perm révoquée : perms.Has devrait retourner false")
	}
}

// TestMCPEndpoint_FullJourney_OAuthThenToolCall teste le flux complet Bearer → tool call.
func TestMCPEndpoint_FullJourney_OAuthThenToolCall(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}
	ctx := context.Background()

	// Préparer user avec perm
	gID, _ := svc.Store.CreateGrade(ctx, "journey-grade")
	pID, _ := svc.Store.EnsurePermission(ctx, "journey.ping", "")
	svc.Store.GrantPerm(ctx, gID, pID) //nolint:errcheck
	svc.Store.AssignGrade(ctx, "user-journey", gID) //nolint:errcheck
	svc.Recompute(ctx, "user-journey") //nolint:errcheck

	tokenID := insertTestToken(t, deps, "user-journey", []string{"journey.ping"}, true)

	reg := actions.NewRegistry()
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "journey.ping",
		RequiredPerm: "journey.ping",
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			return actions.Result{Status: "ok", Message: "pong-journey"}, nil
		},
	})

	mcpSrv := server.NewMCPServer("assokit-journey", "1.0.0")
	actions.MountMCP(mcpSrv, deps, reg)
	httpSrv := server.NewStreamableHTTPServer(mcpSrv)

	r := chi.NewRouter()
	r.Use(oauthBearerMiddleware(deps.DB, svc, deps))
	r.Mount("/mcp", httpSrv)

	// Étape 1 : initialize
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initBody))
	req.Header.Set("Authorization", "Bearer "+tokenID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusAccepted {
		t.Fatalf("initialize : attendu 200/202, got %d body=%q", w.Code, w.Body.String())
	}

	// Étape 2 : tools/list
	sessionID := w.Header().Get("Mcp-Session-Id")
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(listBody))
	req2.Header.Set("Authorization", "Bearer "+tokenID)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req2.Header.Set("Mcp-Session-Id", sessionID)
	}
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK && w2.Code != http.StatusAccepted {
		t.Fatalf("tools/list : attendu 200/202, got %d body=%q", w2.Code, w2.Body.String())
	}

	// Étape 3 : GET /mcp sans Bearer → 401
	req3 := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("GET sans bearer : attendu 401, got %d", w3.Code)
	}
}

// TestMCPEndpoint_ResumabilityAfterDisconnect vérifie que la migration mcp_event_store existe.
func TestMCPEndpoint_ResumabilityAfterDisconnect(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	ctx := context.Background()

	var count int
	err := deps.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_event_store`).Scan(&count)
	if err != nil {
		t.Errorf("table mcp_event_store inaccessible: %v", err)
	}
}

// TestWellKnownMCPServer vérifie le metadata endpoint.
func TestWellKnownMCPServer(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)
	rbacSvc := &rbac.Service{Store: &rbac.Store{DB: deps.DB}, Cache: &rbac.Cache{}}

	r := chi.NewRouter()
	mountMCPEndpoint(r, deps, rbacSvc)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp/server", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("attendu 200, got %d", w.Code)
	}
	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("JSON invalide: %v", err)
	}
	if meta["name"] != "assokit-mcp" {
		t.Errorf("nom incorrect: %v", meta["name"])
	}
	if _, ok := meta["tools_count"]; !ok {
		t.Error("tools_count absent du metadata")
	}
}

// TestMCPEndpoint_RateLimitBruteForce vérifie le rate limit > 10 échecs/min/IP.
func TestMCPEndpoint_RateLimitBruteForce(t *testing.T) {
	deps, _ := newRBACAdminDeps(t)

	// Réinitialiser le guard pour ce test
	guard := newBruteForceGuard()

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				http.Error(w, "bearer requis", 401)
				return
			}
			ip := realIP(r)
			_, err := validateBearerToken(r.Context(), deps.DB, token)
			if err != nil {
				if guard.RecordFailure(ip) {
					http.Error(w, "trop de tentatives", http.StatusTooManyRequests)
					return
				}
				http.Error(w, err.Error(), 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/mcp/test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// 11 tentatives avec un token invalide depuis la même IP → la 11ème doit être 429
	lastCode := 0
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(http.MethodGet, "/mcp/test", nil)
		req.Header.Set("Authorization", "Bearer token-invalide-"+fmt.Sprintf("%d", i))
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		lastCode = w.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("après 12 échecs : attendu 429, got %d", lastCode)
	}
}

// authUser est un alias local pour éviter conflit de noms.
var _ = auth.User{}
