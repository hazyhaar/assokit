package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/perms"
	svcrbac "github.com/hazyhaar/assokit/pkg/rbac"
	"github.com/mark3labs/mcp-go/server"
)

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
//
// Limite assumée (H3) : in-memory, non partagé entre instances. En multi-
// instance (load-balancer), un attaquant peut contourner en répartissant les
// requêtes sur plusieurs instances. Migration prévue vers Redis/SQLite partagé.
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

// bruteForceMaxBuckets borne la taille de la map : chaque IP distincte crée un
// bucket. Sans borne, un attaquant variant son IP (XFF spoof, IPv6 /64) ferait
// croître la map indéfiniment → épuisement mémoire. Au seuil, on purge les
// fenêtres expirées (lazy GC) avant d'insérer.
const bruteForceMaxBuckets = 10000

// RecordFailure enregistre un échec pour l'IP et retourne true si rate-limited.
func (g *bearerBruteForceGuard) RecordFailure(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	b, ok := g.buckets[ip]
	if !ok || now.After(b.windowEnd) {
		if !ok && len(g.buckets) >= bruteForceMaxBuckets {
			g.purgeExpiredLocked(now)
		}
		g.buckets[ip] = &failBucket{count: 1, windowEnd: now.Add(time.Minute)}
		return false
	}
	b.count++
	return b.count > 10
}

// purgeExpiredLocked supprime les buckets dont la fenêtre est expirée.
// Appelant doit détenir g.mu.
func (g *bearerBruteForceGuard) purgeExpiredLocked(now time.Time) {
	for k, b := range g.buckets {
		if now.After(b.windowEnd) {
			delete(g.buckets, k)
		}
	}
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

			ip := clientIP(r, deps.Config.TrustProxyHeaders)
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
			ctx = middleware.ContextWithUser(ctx, &identity.User{ID: info.UserID})
			// Injecter scopes OAuth (clé portée par pkg/actions, lue par l'enforcement MCP).
			ctx = actions.ContextWithScopes(ctx, info.Scopes)

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

// IP cliente extraite via clientIP() (handlers) — honore XFF/X-Real-IP
// uniquement si TrustProxyHeaders, sinon RemoteAddr.

// mountMCPEndpoint monte l'endpoint /mcp sur le router chi.
// Il crée un server MCP, bind toutes les actions, applique le middleware OAuth.
// vault est optionnel (nil = pas de LiveKit) : si non-nil, InitSalonVisio est appelé.
func mountMCPEndpoint(r chi.Router, deps app.AppDeps, rbacSvc *svcrbac.Service, registerActions func(*actions.Registry) error, vault *assets.Vault) error {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)
	seeds.InitSalonVisio(reg, vault)

	// Hook bordure : un consommateur (ex. produit GAFP) injecte ses actions
	// métier dans le MÊME registre que les seeds core → route HTTP + outil MCP
	// + seed de permission RBAC automatiques (LLM-parity). Fail-loud : un ID en
	// doublon (collision avec une action core) remonte une erreur au boot.
	if registerActions != nil {
		if err := registerActions(reg); err != nil {
			return fmt.Errorf("mountMCPEndpoint: register extra actions: %w", err)
		}
	}

	// Seeder les perms RBAC depuis le registry (idempotent), les accorder au
	// grade système admin, puis recomputer les permissions effectives des admins.
	// Sans ce dernier maillon, user_effective_permissions reste vide et Can()
	// renvoie false → 403 sur /admin/actions/{id} malgré le grade admin.
	go func() {
		ctx := context.Background()
		log := deps.Logger
		if log == nil {
			log = slog.Default()
		}
		store := &svcrbac.Store{DB: deps.DB}
		for _, a := range reg.All() {
			permID, err := store.EnsurePermission(ctx, a.RequiredPerm, a.Description)
			if err != nil {
				log.Error("rbac_seed_ensure_permission", "perm", a.RequiredPerm, "err", err.Error())
				continue
			}
			if err := store.GrantPerm(ctx, "sys-admin", permID); err != nil {
				log.Error("rbac_seed_grant_perm", "perm", a.RequiredPerm, "err", err.Error())
			}
		}
		// Perms de ROUTE non portées par une action (gardes perms.Required : lecture
		// RBAC). Sans ce seed, les pages /admin/rbac/* sont 403 pour tous et le
		// tableau de bord les masque à juste titre — mais l'administrateur ne peut
		// alors jamais gérer les droits. Les accorder à sys-admin ferme cet écart.
		for _, p := range []struct{ name, desc string }{
			{"rbac.grades.read", "Consulter les grades et leurs permissions"},
			{"rbac.users.read", "Consulter les comptes et leurs grades"},
			{"rbac.users.write", "Attribuer et retirer des grades aux comptes"},
			{"rbac.audit.read", "Consulter le journal des accès RBAC"},
		} {
			permID, err := store.EnsurePermission(ctx, p.name, p.desc)
			if err != nil {
				log.Error("rbac_seed_ensure_permission", "perm", p.name, "err", err.Error())
				continue
			}
			if err := store.GrantPerm(ctx, "sys-admin", permID); err != nil {
				log.Error("rbac_seed_grant_perm", "perm", p.name, "err", err.Error())
			}
		}
		if rbacSvc != nil {
			rows, err := deps.DB.QueryContext(ctx,
				`SELECT user_id FROM user_grades WHERE grade_id = 'sys-admin'`)
			if err != nil {
				log.Error("rbac_seed_list_sysadmins", "err", err.Error())
				return
			}
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				if err := rbacSvc.Recompute(ctx, id); err != nil {
					log.Error("rbac_seed_recompute", "user_id", id, "err", err.Error())
				}
			}
		}
	}()

	mcpSrv := server.NewMCPServer("assokit-mcp", "1.0.0",
		server.WithToolCapabilities(true),
	)
	actions.MountMCP(mcpSrv, deps, reg)

	// Parité HTTP : chaque action (core + injectée via le hook) obtient aussi
	// GET+POST /admin/actions/{id} — formulaire + exécution — derrière l'auth
	// admin RBAC (requireRBACAdmin) et la permission atomique de l'action
	// (perms.Required dans MountHTTP, qui exige le middleware RBAC actif).
	r.Group(func(r chi.Router) {
		r.Use(requireRBACAdmin(rbacSvc))
		r.Use(middleware.RBAC(rbacSvc))
		actions.MountHTTP(r, deps, reg)
	})

	httpSrv := server.NewStreamableHTTPServer(mcpSrv)

	r.Group(func(r chi.Router) {
		r.Use(oauthBearerMiddleware(deps.DB, rbacSvc, deps))
		r.Mount("/mcp", httpSrv)
	})

	// Discovery metadata
	r.Get("/.well-known/mcp/server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"name":           "assokit-mcp",
			"version":        "1.0.0",
			"oauth_metadata": "/.well-known/oauth-authorization-server",
			"tools_count":    len(reg.All()),
		})
	})

	// RFC 8414 — OAuth 2.0 Authorization Server Metadata
	r.Get("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimRight(deps.Config.BaseURL, "/")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"issuer":                                base,
			"authorization_endpoint":                base + "/oauth/authorize",
			"token_endpoint":                        base + "/oauth/token",
			"registration_endpoint":                 base + "/oauth/register",
			"scopes_supported":                      []string{"openid", "profile", "mcp"},
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		})
	})

	return nil
}

// ScopesFromContext retourne les scopes OAuth depuis le contexte (vue simple,
// délègue à pkg/actions qui porte la clé canonique).
func ScopesFromContext(ctx context.Context) []string {
	scopes, _ := actions.ScopesFromContext(ctx)
	return scopes
}
