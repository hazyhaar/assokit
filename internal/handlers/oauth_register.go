// CLAUDE:SUMMARY Handler POST /oauth2/register — DCR RFC 7591 + rate-limit anti-spam (M-ASSOKIT-DCR-1).
// CLAUDE:WARN Endpoint PUBLIC (pas de Bearer requis) — claude.ai web doit pouvoir s'auto-register.
package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	intoauth "github.com/hazyhaar/assokit/internal/oauth"
)

// dcrRateLimiter : 5 register/IP/heure. In-memory simple, bucket fixe glissant.
type dcrRateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func newDCRRateLimiter() *dcrRateLimiter {
	return &dcrRateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   5,
		window:  time.Hour,
	}
}

// Allow retourne true si le quota n'est pas dépassé pour cette IP.
func (r *dcrRateLimiter) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-r.window)
	stamps := r.buckets[ip]
	// Garbage collect entries hors window.
	kept := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.limit {
		r.buckets[ip] = kept
		return false
	}
	kept = append(kept, now)
	r.buckets[ip] = kept
	return true
}

var globalDCRRateLimiter = newDCRRateLimiter()

// OAuth2RegisterHandler POST /oauth2/register : RFC 7591.
// Pas d'auth — endpoint public pour permettre claude.ai web (et autres clients
// MCP standards) de s'auto-register dynamiquement.
// Rate-limit 5/h/IP anti-spam.
func OAuth2RegisterHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPFromRequest(r)
		if !globalDCRRateLimiter.Allow(ip) {
			deps.Logger.Warn("dcr_rate_limited", "ip_hash", maskIP(ip))
			http.Error(w, "rate limit exceeded (5/heure)", http.StatusTooManyRequests)
			return
		}

		var req intoauth.RegisterRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
			http.Error(w, "JSON invalide: "+err.Error(), http.StatusBadRequest)
			return
		}

		resp, err := intoauth.Register(r.Context(), deps.DB, req)
		if err != nil {
			deps.Logger.Warn("dcr_register_failed", "ip_hash", maskIP(ip), "err", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		deps.Logger.Info("dcr_client_registered",
			"client_id", resp.ClientID,
			"public", req.IsPublicClient(),
			"client_name", req.ClientName,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

// clientIPFromRequest : X-Forwarded-For ou RemoteAddr.
func clientIPFromRequest(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// Premier élément en cas de chain.
		if idx := indexComma(ip); idx > 0 {
			return ip[:idx]
		}
		return ip
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func indexComma(s string) int {
	for i, c := range s {
		if c == ',' {
			return i
		}
	}
	return -1
}

// maskIP retourne un hash court pour logs (pas de plaintext IP).
func maskIP(ip string) string {
	if len(ip) <= 4 {
		return "***"
	}
	return ip[:3] + "***"
}

// resetDCRRateLimiter : helper test pour vider le rate limiter entre runs.
func resetDCRRateLimiter() {
	globalDCRRateLimiter = newDCRRateLimiter()
}
