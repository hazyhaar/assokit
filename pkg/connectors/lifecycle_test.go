// CLAUDE:SUMMARY Tests gardiens Lifecycle — Start/Stop boot, ping routine, health tracking (M-ASSOKIT-SPRINT2-S1).
package connectors

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// trackingConnector compte Start/Stop/Ping pour assertions.
type trackingConnector struct {
	id        string
	starts    atomic.Int32
	stops     atomic.Int32
	pings     atomic.Int32
	startErr  error
	pingErr   error
	pingHealth Health
}

func (t *trackingConnector) ID() string                       { return t.id }
func (t *trackingConnector) DisplayName() string              { return t.id }
func (t *trackingConnector) Description() string              { return "tracking" }
func (t *trackingConnector) ConfigSchema() *jsonschema.Schema { return nil }
func (t *trackingConnector) Start(ctx context.Context, cfg map[string]any) error {
	t.starts.Add(1)
	return t.startErr
}
func (t *trackingConnector) Stop(ctx context.Context) error {
	t.stops.Add(1)
	return nil
}
func (t *trackingConnector) Ping(ctx context.Context) (Health, error) {
	t.pings.Add(1)
	if t.pingErr != nil {
		return Health{OK: false, Message: t.pingErr.Error()}, t.pingErr
	}
	if t.pingHealth.Message == "" {
		return Health{OK: true, Message: "OK"}, nil
	}
	return t.pingHealth, nil
}
func (t *trackingConnector) HandleWebhook(ctx context.Context, eventType string, payload []byte) error {
	return nil
}

func openLifecycleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connectors (
			id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}',
			configured_at TEXT, configured_by TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestLifecycle_StartAllConfiguredConnectorsAtBoot : enabled=1 → Start() appelé, enabled=0 → non.
func TestLifecycle_StartAllConfiguredConnectorsAtBoot(t *testing.T) {
	db := openLifecycleDB(t)
	if _, err := db.Exec(`
		INSERT INTO connectors(id, enabled, config_json) VALUES
			('on', 1, '{}'),
			('off', 0, '{}');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reg := NewRegistry()
	cOn := &trackingConnector{id: "on"}
	cOff := &trackingConnector{id: "off"}
	_ = reg.Register(cOn)
	_ = reg.Register(cOff)

	l := &Lifecycle{Registry: reg, DB: db}
	if err := l.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	if cOn.starts.Load() != 1 {
		t.Errorf("connector 'on' Start count=%d, attendu 1", cOn.starts.Load())
	}
	if cOff.starts.Load() != 0 {
		t.Errorf("connector 'off' Start count=%d, attendu 0 (enabled=0)", cOff.starts.Load())
	}
}

// TestLifecycle_StopAllOnShutdown : tous les connectors started reçoivent Stop().
func TestLifecycle_StopAllOnShutdown(t *testing.T) {
	db := openLifecycleDB(t)
	db.Exec(`INSERT INTO connectors(id, enabled) VALUES('a', 1), ('b', 1);`) //nolint:errcheck

	reg := NewRegistry()
	cA := &trackingConnector{id: "a"}
	cB := &trackingConnector{id: "b"}
	_ = reg.Register(cA)
	_ = reg.Register(cB)

	l := &Lifecycle{Registry: reg, DB: db}
	_ = l.StartAll(context.Background())
	l.StopAll(context.Background())

	if cA.stops.Load() != 1 || cB.stops.Load() != 1 {
		t.Errorf("Stop counts: a=%d b=%d, attendu 1 chacun", cA.stops.Load(), cB.stops.Load())
	}
}

// TestLifecycle_PingAllUpdatesHealth : Ping() met à jour healths map.
func TestLifecycle_PingAllUpdatesHealth(t *testing.T) {
	db := openLifecycleDB(t)
	db.Exec(`INSERT INTO connectors(id, enabled) VALUES('p', 1);`) //nolint:errcheck

	reg := NewRegistry()
	c := &trackingConnector{id: "p", pingHealth: Health{OK: true, Message: "Custom"}}
	_ = reg.Register(c)

	l := &Lifecycle{Registry: reg, DB: db}
	_ = l.StartAll(context.Background())
	l.PingAll(context.Background())

	h := l.Health("p")
	if !h.OK || h.Message != "Custom" {
		t.Errorf("Health(p) = %+v, attendu OK+Custom", h)
	}
	if h.LastCheck.IsZero() {
		t.Error("LastCheck non posé")
	}
	l.StopAll(context.Background())
}

// TestLifecycle_PingFailureMarksUnhealthy : Ping err → Health.OK=false.
func TestLifecycle_PingFailureMarksUnhealthy(t *testing.T) {
	db := openLifecycleDB(t)
	db.Exec(`INSERT INTO connectors(id, enabled) VALUES('bad', 1);`) //nolint:errcheck

	reg := NewRegistry()
	c := &trackingConnector{id: "bad", pingErr: errors.New("token expired")}
	_ = reg.Register(c)

	l := &Lifecycle{Registry: reg, DB: db}
	_ = l.StartAll(context.Background())
	l.PingAll(context.Background())

	h := l.Health("bad")
	if h.OK {
		t.Errorf("Health(bad) OK=true, attendu false")
	}
	if h.Message == "" {
		t.Error("Health(bad).Message vide")
	}
	l.StopAll(context.Background())
}

// TestLifecycle_PingRoutineFiresPeriodicallyEvery : PingInterval court → ≥2 pings sur fenêtre.
func TestLifecycle_PingRoutineFiresPeriodicallyEvery(t *testing.T) {
	db := openLifecycleDB(t)
	db.Exec(`INSERT INTO connectors(id, enabled) VALUES('tick', 1);`) //nolint:errcheck

	reg := NewRegistry()
	c := &trackingConnector{id: "tick"}
	_ = reg.Register(c)

	l := &Lifecycle{Registry: reg, DB: db, PingInterval: 30 * time.Millisecond}
	_ = l.StartAll(context.Background())
	time.Sleep(100 * time.Millisecond)
	l.StopAll(context.Background())

	pings := c.pings.Load()
	if pings < 2 {
		t.Errorf("Ping count=%d sur 100ms tick=30ms, attendu ≥2", pings)
	}
}

// TestLifecycle_StartUnregisteredConnectorSkipsWithWarn : DB liste un id pas dans Registry → skip + log warn.
func TestLifecycle_StartUnregisteredConnectorSkipsWithWarn(t *testing.T) {
	db := openLifecycleDB(t)
	db.Exec(`INSERT INTO connectors(id, enabled) VALUES('ghost', 1);`) //nolint:errcheck

	reg := NewRegistry()
	l := &Lifecycle{Registry: reg, DB: db}
	if err := l.StartAll(context.Background()); err != nil {
		t.Errorf("StartAll devrait skip silencieusement, got err=%v", err)
	}
}
