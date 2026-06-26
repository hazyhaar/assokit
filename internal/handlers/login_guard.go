// CLAUDE:SUMMARY Verrouillage anti-bruteforce du login mot de passe : N échecs/fenêtre par identité -> blocage temporaire. Map bornée (pattern S2). M-ASSOKIT-LOGIN-LOCKOUT.
package handlers

import (
	"strings"
	"sync"
	"time"
)

// loginGuard verrouille temporairement une identité après trop d'échecs de
// connexion consécutifs, pour contrer le credential-stuffing / brute-force.
// Clé = email normalisé (verrou par compte). In-memory et BORNÉE : au seuil,
// les entrées expirées sont purgées (anti-DoS mémoire, cf. dcrRateLimiter S2).
type loginGuard struct {
	mu       sync.Mutex
	fails    map[string]*loginFail
	maxFails int
	window   time.Duration // fenêtre d'accumulation des échecs
	lockDur  time.Duration // durée du verrou une fois le seuil atteint
}

type loginFail struct {
	count     int
	firstAt   time.Time
	lockUntil time.Time
}

const loginGuardMaxKeys = 10000

func newLoginGuard() *loginGuard {
	return &loginGuard{
		fails:    make(map[string]*loginFail),
		maxFails: 5,
		window:   15 * time.Minute,
		lockDur:  15 * time.Minute,
	}
}

func loginKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Locked indique si l'identité est verrouillée et, le cas échéant, le temps
// restant avant déverrouillage.
func (g *loginGuard) Locked(email string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, ok := g.fails[loginKey(email)]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if f.lockUntil.After(now) {
		return true, time.Until(f.lockUntil).Round(time.Minute)
	}
	return false, 0
}

// RecordFailure enregistre un échec de connexion. Au-delà de maxFails dans la
// fenêtre, pose un verrou de lockDur.
func (g *loginGuard) RecordFailure(email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	key := loginKey(email)
	f, ok := g.fails[key]
	if !ok || now.Sub(f.firstAt) > g.window {
		if !ok && len(g.fails) >= loginGuardMaxKeys {
			g.purgeExpiredLocked(now)
		}
		g.fails[key] = &loginFail{count: 1, firstAt: now}
		return
	}
	f.count++
	if f.count >= g.maxFails {
		f.lockUntil = now.Add(g.lockDur)
	}
}

// Reset efface l'historique d'une identité (à appeler après une connexion réussie).
func (g *loginGuard) Reset(email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, loginKey(email))
}

// purgeExpiredLocked supprime les entrées hors fenêtre et déverrouillées.
// Appelant doit détenir g.mu.
func (g *loginGuard) purgeExpiredLocked(now time.Time) {
	for k, f := range g.fails {
		if f.lockUntil.Before(now) && now.Sub(f.firstAt) > g.window {
			delete(g.fails, k)
		}
	}
}

var globalLoginGuard = newLoginGuard()
