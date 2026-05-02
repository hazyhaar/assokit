// CLAUDE:SUMMARY Registry des connecteurs tiers — Register/Get/All thread-safe (M-ASSOKIT-SPRINT2-S1).
package connectors

import (
	"fmt"
	"sort"
	"sync"
)

// Registry collecte les Connectors disponibles.
type Registry struct {
	mu         sync.RWMutex
	connectors map[string]Connector
}

// NewRegistry crée un Registry vide.
func NewRegistry() *Registry {
	return &Registry{connectors: make(map[string]Connector)}
}

// Register ajoute un connecteur. Erreur si l'ID est déjà pris.
func (r *Registry) Register(c Connector) error {
	if c == nil {
		return fmt.Errorf("connectors: Register(nil)")
	}
	id := c.ID()
	if id == "" {
		return fmt.Errorf("connectors: Connector.ID() vide")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connectors[id]; exists {
		return fmt.Errorf("%w : id=%q", ErrDuplicateConnector, id)
	}
	r.connectors[id] = c
	return nil
}

// Get retourne le connecteur enregistré pour cet ID.
func (r *Registry) Get(id string) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[id]
	return c, ok
}

// All retourne tous les connecteurs triés par ID (déterministe pour UI).
func (r *Registry) All() []Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Connector, 0, len(r.connectors))
	for _, c := range r.connectors {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
