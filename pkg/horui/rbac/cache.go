// CLAUDE:SUMMARY Cache L1 in-memory version-based pour effective permissions RBAC (M-ASSOKIT-RBAC-2).
// CLAUDE:WARN Version atomique : toute mutation doit appeler BumpVersion(). Entrée invalide si entry.version < current.
package rbac

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

const defaultCacheMaxUsers = 10_000

// cacheEntry est une entrée L1 : perms par nom + version au moment du chargement.
type cacheEntry struct {
	perms   map[string]struct{} // clés = permission names (atoms)
	version uint64
}

// Cache est le cache L1 in-memory des permissions effectives.
// Invalidation par version atomique : mutation → BumpVersion() → entrées stalées automatiquement.
// MaxSize borne le nombre d'entrées L1 (0 = defaultCacheMaxUsers) pour éviter les memory leaks.
type Cache struct {
	ver     atomic.Uint64
	entries sync.Map     // string (userID) → *cacheEntry
	size    atomic.Int64 // count approximatif d'entrées actives
	MaxSize int64        // 0 = defaultCacheMaxUsers
}

func (c *Cache) maxUsers() int64 {
	if c.MaxSize > 0 {
		return c.MaxSize
	}
	return defaultCacheMaxUsers
}

// CurrentVersion retourne la version globale courante.
func (c *Cache) CurrentVersion() uint64 {
	return c.ver.Load()
}

// BumpVersion incrémente la version globale et invalide toutes les entrées L1.
// Retourne la nouvelle version.
func (c *Cache) BumpVersion() uint64 {
	return c.ver.Add(1)
}

// Get retourne les permissions effectives d'un user si le cache L1 est valide.
// Retourne (nil, false) si absent ou stale (version mismatch).
func (c *Cache) Get(userID string) (map[string]struct{}, bool) {
	v, ok := c.entries.Load(userID)
	if !ok {
		return nil, false
	}
	e := v.(*cacheEntry)
	if e.version != c.ver.Load() {
		return nil, false
	}
	return e.perms, true
}

// Set stocke les permissions effectives d'un user avec la version courante.
// Skip silencieux si le cache dépasse MaxSize (borne mémoire).
func (c *Cache) Set(userID string, perms map[string]struct{}) {
	if c.size.Load() >= c.maxUsers() {
		return
	}
	_, loaded := c.entries.Swap(userID, &cacheEntry{
		perms:   perms,
		version: c.ver.Load(),
	})
	if !loaded {
		c.size.Add(1)
	}
}

// Invalidate supprime l'entrée L1 pour un user (force rechargement depuis L2).
func (c *Cache) Invalidate(userID string) {
	if _, loaded := c.entries.LoadAndDelete(userID); loaded {
		c.size.Add(-1)
	}
}

// StartAuditSync lance un goroutine qui bumpe la version si rbac_audit a de nouvelles entrées.
// Utile en multi-process futur ; no-op si pas d'entrées récentes.
func (c *Cache) StartAuditSync(ctx context.Context, db *sql.DB, interval time.Duration) {
	go func() {
		lastSync := time.Now().UTC()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				var count int
				_ = db.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM rbac_audit WHERE created_at > ?`,
					lastSync.Format(time.RFC3339)).Scan(&count)
				if count > 0 {
					c.BumpVersion()
				}
				lastSync = t.UTC()
			}
		}
	}()
}
