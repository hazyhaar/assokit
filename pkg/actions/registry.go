package actions

import (
	"errors"
	"sort"
)

// ErrDuplicateActionID est retourné quand deux actions ont le même ID.
var ErrDuplicateActionID = errors.New("actions: duplicate action ID")

// Registry est le catalogue d'actions.
type Registry struct {
	actions map[string]Action
}

// NewRegistry crée un Registry vide.
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]Action)}
}

// Add enregistre une action. Retourne ErrDuplicateActionID si l'ID existe déjà.
func (r *Registry) Add(a Action) error {
	if _, exists := r.actions[a.ID]; exists {
		return ErrDuplicateActionID
	}
	r.actions[a.ID] = a
	return nil
}

// Get retourne l'action par ID.
func (r *Registry) Get(id string) (Action, bool) {
	a, ok := r.actions[id]
	return a, ok
}

// All retourne toutes les actions triées par ID.
func (r *Registry) All() []Action {
	result := make([]Action, 0, len(r.actions))
	for _, a := range r.actions {
		result = append(result, a)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}
