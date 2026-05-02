package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	svcrbac "github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/mark3labs/mcp-go/server"
)

// mcpScopesKey est la clé de contexte pour les scopes OAuth du token Bearer courant.
type mcpScopesKey struct{}

var (
	errInvalidToken = errors.New("token invalide ou révoqué")
	errTokenExpired = errors.New("token expiré")
)

// tokenInfo contient les informations d'un token Bearer validé.
type tokenInfo struct {
	UserID string
	Scopes []string
}

// validateBearerToken vérifie un Bearer token OAuth contre la table oauth_tokens via son SHA256.
func validateBearerToken(ctx context.Context, db *sql.DB, token string) (*tokenInfo, error) {
	tokenHash := hashBearerToken(token)
	var userID, rawScopes, expiresAt string
	err := db.QueryRowContext(ctx,
		`SELECT user_id, scopes, expires_at FROM oauth_tokens WHERE access_token_hash=? AND revoked_at IS NULL`,
		tokenHash,
	).Scan(&userID, &rawScopes, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvalidToken
	}
	if err != nil {
		return nil, err
	}

	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		return nil, errTokenExpired
	}

	var scopes []string
	json.Unmarshal([]byte(rawScopes), &scopes) //nolint:errcheck

	return &tokenInfo{UserID: userID, Scopes: scopes}, nil
}

// bearerBruteForceGuard comptabilise les échecs Bearer par IP.
type bearerBruteForceGuard struct {
	mu      sync.Mutex
	buckets map[string]*failBucket
}

type failBucket struct {
	count     int
	windowEnd time.Time
}

func newBruteForceGuard() *bearerBruteForceGuard {
	return &bearerBruteForceGuard{buckets: make(map[string]*failBucket)}
}

// RecordFailure enregistre un échec pour l'IP et retourne true si rate-limited.
func (g *bearerBruteForceGuard) RecordFailure(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	b, ok := g.buckets[ip]
	if !ok || now.After(b.windowEnd) {
		g.buckets[ip] = &failBucket{count: 1, windowEnd: now.Add(time.Minute)}
		return false
	}
	b.count++
	return b.count > 10
}

var globalBruteForce = newBruteForceGuard()

func hashBearerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// oauthBearerMiddleware vérifie le Bearer token OAuth, retourne 401 si absent/invalide,
// injecte userID + scopes + rbac.Service dans le contexte si valide.
func oauthBearerMiddleware(db *sql.DB, rbacSvc *svcrbac.Service, deps app.AppDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := middleware.RequestIDFromContext(ctx)
			token := extractBearer(r)
			if token == "" {
				deps.Logger.Warn("mcp_bearer_missing", "req_id", reqID)
				w.Header().Set("WWW-Authenticate", `Bearer realm="assokit-mcp"`)
				http.Error(w, "Bearer token requis", http.StatusUnauthorized)
				return
			}

			ip := realIP(r)
			ipHashShort := middleware.HashIP(ip, deps.Config.CookieSecret)
			info, err := validateBearerToken(ctx, db, token)
			if err != nil {
				deps.Logger.Warn("mcp_bearer_invalid",
					"req_id", reqID,
					"ip_hash_prefix", ipHashShort,
					"reason", err.Error(),
				)
				if globalBruteForce.RecordFailure(ip) {
					deps.Logger.Warn("mcp_bearer_brute_force_blocked",
						"req_id", reqID,
						"ip_hash_prefix", ipHashShort,
					)
					http.Error(w, "trop de tentatives", http.StatusTooManyRequests)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="assokit-mcp" error="invalid_token"`)
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			// Injecter pour perms.Has (RBAC service + userID)
			ctx = perms.ContextWithService(ctx, rbacSvc)
			ctx = perms.ContextWithUserID(ctx, info.UserID)
			// Injecter user pour middleware.UserFromContext (audit MCP)
			ctx = middleware.ContextWithUser(ctx, &auth.User{ID: info.UserID})
			// Injecter scopes OAuth
			ctx = context.WithValue(ctx, mcpScopesKey{}, info.Scopes)

			deps.Logger.Info("mcp_bearer_validated",
				"req_id", reqID,
				"user_id", info.UserID,
				"scopes", info.Scopes,
			)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	return r.RemoteAddr
}

// mountMCPEndpoint monte l'endpoint /mcp sur le router chi.
// Il crée un server MCP, bind toutes les actions, applique le middleware OAuth.
func mountMCPEndpoint(r chi.Router, deps app.AppDeps, rbacSvc *svcrbac.Service) {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)

	// Seeder les perms RBAC depuis le registry (idempotent)
	go func() {
		ctx := context.Background()
		store := &svcrbac.Store{DB: deps.DB}
		for _, a := range reg.All() {
			store.EnsurePermission(ctx, a.RequiredPerm, a.Description) //nolint:errcheck
		}
	}()

	mcpSrv := server.NewMCPServer("assokit-mcp", "1.0.0",
		server.WithToolCapabilities(true),
	)
	actions.MountMCP(mcpSrv, deps, reg)

	httpSrv := server.NewStreamableHTTPServer(mcpSrv)

	r.Group(func(r chi.Router) {
		r.Use(oauthBearerMiddleware(deps.DB, rbacSvc, deps))
		r.Mount("/mcp", httpSrv)
	})

	// Discovery metadata
	r.Get("/.well-known/mcp/server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"name":             "assokit-mcp",
			"version":          "1.0.0",
			"oauth_metadata":   "/.well-known/oauth-authorization-server",
			"tools_count":      len(reg.All()),
		})
	})
}

// ScopesFromContext retourne les scopes OAuth depuis le contexte.
func ScopesFromContext(ctx context.Context) []string {
	scopes, _ := ctx.Value(mcpScopesKey{}).([]string)
	return scopes
}
