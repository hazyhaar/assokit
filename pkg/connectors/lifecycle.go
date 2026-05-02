// CLAUDE:SUMMARY Lifecycle Connector — Start/Stop boot/shutdown + ping routine périodique (M-ASSOKIT-SPRINT2-S1).
// CLAUDE:WARN PingInterval défaut 5min ; passer 0 désactive le routine. Lifecycle.healths protégée mu.
package connectors

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Lifecycle gère le cycle de vie des Connectors enabled en DB.
type Lifecycle struct {
	Registry     *Registry
	DB           *sql.DB
	Logger       *slog.Logger
	PingInterval time.Duration

	mu      sync.RWMutex
	healths map[string]Health
	started []string // IDs des connectors actuellement en cours
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func (l *Lifecycle) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

// StartAll lit la table connectors et appelle Start() sur chaque enabled=1.
// Démarre aussi la goroutine de healthcheck périodique si PingInterval > 0.
func (l *Lifecycle) StartAll(ctx context.Context) error {
	if l.Registry == nil || l.DB == nil {
		return fmt.Errorf("connectors.Lifecycle: Registry+DB requis")
	}
	l.mu.Lock()
	if l.healths == nil {
		l.healths = make(map[string]Health)
	}
	l.mu.Unlock()

	rows, err := l.DB.QueryContext(ctx,
		`SELECT id, config_json FROM connectors WHERE enabled = 1`)
	if err != nil {
		return fmt.Errorf("connectors.StartAll: query: %w", err)
	}
	type entry struct{ id, cfgJSON string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.cfgJSON); err != nil {
			rows.Close()
			return fmt.Errorf("connectors.StartAll: scan: %w", err)
		}
		entries = append(entries, e)
	}
	rows.Close()

	for _, e := range entries {
		c, ok := l.Registry.Get(e.id)
		if !ok {
			l.logger().Warn("connector_not_registered", "id", e.id)
			continue
		}
		var cfg map[string]any
		if e.cfgJSON != "" {
			_ = json.Unmarshal([]byte(e.cfgJSON), &cfg)
		}
		if cfg == nil {
			cfg = map[string]any{}
		}
		if err := c.Start(ctx, cfg); err != nil {
			l.logger().Error("connector_start_failed", "id", e.id, "err", err.Error())
			continue
		}
		l.mu.Lock()
		l.started = append(l.started, e.id)
		l.mu.Unlock()
		l.logger().Info("connector_started", "id", e.id)
	}

	if l.PingInterval > 0 {
		pingCtx, cancel := context.WithCancel(context.Background())
		l.mu.Lock()
		l.cancel = cancel
		l.mu.Unlock()
		l.wg.Add(1)
		go l.runPingLoop(pingCtx)
	}
	return nil
}

// StopAll appelle Stop() sur chaque connector démarré + arrête la routine ping.
func (l *Lifecycle) StopAll(ctx context.Context) {
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	started := append([]string(nil), l.started...)
	l.started = nil
	l.mu.Unlock()
	l.wg.Wait()

	for _, id := range started {
		c, ok := l.Registry.Get(id)
		if !ok {
			continue
		}
		if err := c.Stop(ctx); err != nil {
			l.logger().Error("connector_stop_failed", "id", id, "err", err.Error())
		} else {
			l.logger().Info("connector_stopped", "id", id)
		}
	}
}

// Health retourne le dernier état connu d'un connector (ou Health vide si jamais pingé).
func (l *Lifecycle) Health(id string) Health {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.healths[id]
}

// PingAll exécute Ping() sur tous les connectors started, met à jour healths.
func (l *Lifecycle) PingAll(ctx context.Context) {
	l.mu.RLock()
	started := append([]string(nil), l.started...)
	l.mu.RUnlock()

	for _, id := range started {
		c, ok := l.Registry.Get(id)
		if !ok {
			continue
		}
		start := time.Now()
		h, err := c.Ping(ctx)
		h.LastCheck = time.Now()
		if h.Latency == 0 {
			h.Latency = time.Since(start)
		}
		if err != nil {
			h.OK = false
			if h.Message == "" {
				h.Message = err.Error()
			}
			l.logger().Warn("connector_ping_failed", "id", id, "err", err.Error())
		}
		l.mu.Lock()
		l.healths[id] = h
		l.mu.Unlock()
	}
}

func (l *Lifecycle) runPingLoop(ctx context.Context) {
	defer l.wg.Done()
	ticker := time.NewTicker(l.PingInterval)
	defer ticker.Stop()
	l.PingAll(ctx) // initial tick immediate
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.PingAll(ctx)
		}
	}
}
