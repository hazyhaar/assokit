// CLAUDE:SUMMARY RateLimiter sliding window in-memory par clé (3 POST/5min). Context helper PageURI.
package middleware

import (
	"context"
	"sync"
	"time"
)

// --- Context helper : URI de la page courante ---

type ctxPageURIKey struct{}

// WithPageURI injecte l'URI de la page courante dans le contexte.
func WithPageURI(ctx context.Context, uri string) context.Context {
	return context.WithValue(ctx, ctxPageURIKey{}, uri)
}

// PageURIFromContext retourne l'URI de la page courante (chaîne vide si absent).
func PageURIFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxPageURIKey{}).(string)
	return v
}

// --- Rate limiter sliding window ---

const (
	RateLimitMax    = 3
	RateLimitWindow = 5 * time.Minute
)

// RateLimiter comptabilise les accès par clé dans une fenêtre glissante.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

// NewRateLimiter crée un RateLimiter avec goroutine de nettoyage toutes les minutes.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{entries: make(map[string][]time.Time)}
	go rl.cleanupLoop()
	return rl
}

// Allow retourne true si la clé est en dessous de la limite ; enregistre l'accès.
// Retourne false sans enregistrer si la limite est dépassée.
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-RateLimitWindow)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	times := rl.entries[key]
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= RateLimitMax {
		rl.entries[key] = valid
		return false
	}
	rl.entries[key] = append(valid, now)
	return true
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-RateLimitWindow)
		rl.mu.Lock()
		for key, times := range rl.entries {
			valid := times[:0]
			for _, t := range times {
				if t.After(cutoff) {
					valid = append(valid, t)
				}
			}
			if len(valid) == 0 {
				delete(rl.entries, key)
			} else {
				rl.entries[key] = valid
			}
		}
		rl.mu.Unlock()
	}
}
