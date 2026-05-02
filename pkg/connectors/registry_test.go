// CLAUDE:SUMMARY Tests gardiens Registry — duplicate refused, Get/All (M-ASSOKIT-SPRINT2-S1).
package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// stubConnector : connecteur factice pour tests.
type stubConnector struct {
	id   string
	name string
}

func (s *stubConnector) ID() string                                        { return s.id }
func (s *stubConnector) DisplayName() string                               { return s.name }
func (s *stubConnector) Description() string                               { return "stub for tests" }
func (s *stubConnector) ConfigSchema() *jsonschema.Schema                  { return nil }
func (s *stubConnector) Start(ctx context.Context, cfg map[string]any) error { return nil }
func (s *stubConnector) Stop(ctx context.Context) error                    { return nil }
func (s *stubConnector) Ping(ctx context.Context) (Health, error)          { return Health{OK: true, Message: "OK"}, nil }

func TestRegistry_RegisterDuplicateRefused(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubConnector{id: "foo", name: "Foo"}); err != nil {
		t.Fatalf("première Register: %v", err)
	}
	err := r.Register(&stubConnector{id: "foo", name: "FooBis"})
	if !errors.Is(err, ErrDuplicateConnector) {
		t.Errorf("seconde Register err = %v, attendu ErrDuplicateConnector", err)
	}
}

func TestRegistry_RegisterNilOrEmptyIDRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("Register(nil) devrait échouer")
	}
	if err := r.Register(&stubConnector{id: "", name: "X"}); err == nil {
		t.Error("Register avec ID vide devrait échouer")
	}
}

func TestRegistry_GetReturnsConnector(t *testing.T) {
	r := NewRegistry()
	c := &stubConnector{id: "bar", name: "Bar"}
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("bar")
	if !ok {
		t.Fatal("Get(bar) ok=false")
	}
	if got.ID() != "bar" {
		t.Errorf("Get(bar).ID() = %q, attendu bar", got.ID())
	}
}

func TestRegistry_GetUnknownReturnsNotFound(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("inconnu"); ok {
		t.Error("Get(inconnu) ok=true, attendu false")
	}
}

func TestRegistry_AllSortedByID(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"zebra", "alpha", "mango"} {
		_ = r.Register(&stubConnector{id: id, name: id})
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() len=%d, attendu 3", len(all))
	}
	if all[0].ID() != "alpha" || all[1].ID() != "mango" || all[2].ID() != "zebra" {
		t.Errorf("All() ordre = [%s, %s, %s], attendu [alpha, mango, zebra]",
			all[0].ID(), all[1].ID(), all[2].ID())
	}
}
