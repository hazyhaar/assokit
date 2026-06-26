// CLAUDE:SUMMARY Handler POST /oauth2/register — DCR RFC 7591 + rate-limit anti-spam (M-ASSOKIT-DCR-1).
// CLAUDE:WARN Endpoint PUBLIC (pas de Bearer requis) — claude.ai web doit pouvoir s'auto-register.
package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
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

	// Borne mémoire : une IP qui s'enregistre une fois puis ne revient jamais
	// laisserait son bucket en place indéfiniment. Au seuil, on purge toutes les
	// IPs dont la dernière frappe est hors fenêtre.
	if _, exists := r.buckets[ip]; !exists && len(r.buckets) >= dcrRateLimiterMaxBuckets {
		for k, stamps := range r.buckets {
			if len(stamps) == 0 || !stamps[len(stamps)-1].After(cutoff) {
				delete(r.buckets, k)
			}
		}
	}

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

const dcrRateLimiterMaxBuckets = 10000

var globalDCRRateLimiter = newDCRRateLimiter()

// OAuth2RegisterHandler POST /oauth2/register : RFC 7591.
// Pas d'auth — endpoint public pour permettre claude.ai web (et autres clients
// MCP standards) de s'auto-register dynamiquement.
// Rate-limit 5/h/IP anti-spam.
func OAuth2RegisterHandler(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, deps.Config.TrustProxyHeaders)
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

		// Purge opportuniste des clients DCR abandonnés (TTL, sans token actif).
		// Best-effort : un échec ne bloque pas l'enregistrement. Borné par le
		// rate-limit 5/h/IP en amont.
		if n, perr := intoauth.PurgeStaleClients(r.Context(), deps.DB, time.Now()); perr != nil {
			deps.Logger.Warn("dcr_purge_failed", "err", perr.Error())
		} else if n > 0 {
			deps.Logger.Info("dcr_clients_purged", "count", n)
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

// clientIP retourne l'IP cliente pour les rate-limits / anti-bruteforce.
//
// SÉCURITÉ : X-Forwarded-For / X-Real-IP ne sont honorés QUE si trustProxy est
// vrai (instance derrière un reverse-proxy de confiance qui réécrit ces
// en-têtes). Sinon ils sont spoofables par n'importe quel client et
// contourneraient tous les rate-limits → on se rabat sur RemoteAddr.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		// X-Real-IP est posé par le reverse-proxy de confiance à $remote_addr (le
		// pair réel) et écrase toute valeur fournie par le client : non spoofable.
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		// Repli X-Forwarded-For : avec un proxy unique de confiance qui APPEND le
		// pair réel ($proxy_add_x_forwarded_for), l'IP cliente est l'élément le
		// PLUS À DROITE — le seul que le proxy ait ajouté. Prendre le premier
		// élément serait spoofable (le client peut préfixer une fausse IP et
		// contourner les rate-limits).
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			parts := strings.Split(ip, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
