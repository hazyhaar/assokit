package middleware

import (
	"expvar"
	"log/slog"
	"net/http"
	"time"
)

// Métriques d'exposition (expvar, servies sur /debug/vars). Observabilité minimale
// sans dépendance externe : volume de requêtes, réponses en erreur, requêtes en vol.
var (
	metricRequestsTotal = expvar.NewInt("http_requests_total")
	metricResponses5xx  = expvar.NewInt("http_responses_5xx")
	metricResponses4xx  = expvar.NewInt("http_responses_4xx")
	metricInFlight      = expvar.NewInt("http_requests_in_flight")
)

// statusRecorder capture le code de statut écrit par le handler en aval, pour le
// journaliser (http.ResponseWriter ne l'expose pas autrement).
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

// AccessLog journalise chaque requête HTTP (méthode, chemin, statut, durée, octets,
// req_id) en slog structuré et alimente les compteurs expvar. À placer tôt dans la
// chaîne (après RequestID pour disposer du req_id, en amont de Recoverer pour
// capturer aussi le 500 d'une panique récupérée). Trou d'observabilité comblé
// (audit 2026-06-13 : aucun journal d'accès centralisé auparavant).
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			metricRequestsTotal.Add(1)
			metricInFlight.Add(1)
			sr := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(sr, r)

			metricInFlight.Add(-1)
			if sr.status == 0 {
				sr.status = http.StatusOK
			}
			switch {
			case sr.status >= 500:
				metricResponses5xx.Add(1)
			case sr.status >= 400:
				metricResponses4xx.Add(1)
			}
			logger.Info("http_access",
				"req_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", sr.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", sr.bytes,
			)
		})
	}
}
