package seeds_test

import (
	"testing"

	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
)

// TestInitAll_NoErrors vérifie que InitAll ne panique pas et retourne 30+ actions.
func TestInitAll_NoErrors(t *testing.T) {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)

	all := reg.All()
	if len(all) < 30 {
		t.Errorf("InitAll : attendu >= 30 actions, got %d", len(all))
	}
}

// TestInitAll_NoDuplicates vérifie qu'il n'y a pas de doublon d'ID entre domaines.
func TestInitAll_NoDuplicates(t *testing.T) {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)

	seen := make(map[string]int)
	for _, a := range reg.All() {
		seen[a.ID]++
		if seen[a.ID] > 1 {
			t.Errorf("ID dupliqué détecté : %q", a.ID)
		}
	}
}

// TestInitAll_AllHaveRequiredPerm vérifie que toutes les actions ont un RequiredPerm.
func TestInitAll_AllHaveRequiredPerm(t *testing.T) {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)

	for _, a := range reg.All() {
		if a.RequiredPerm == "" {
			t.Errorf("action %q : RequiredPerm vide", a.ID)
		}
	}
}

// TestInitAll_AllHaveParamsSchema vérifie que toutes les actions ont un ParamsSchema non-nil.
func TestInitAll_AllHaveParamsSchema(t *testing.T) {
	reg := actions.NewRegistry()
	seeds.InitAll(reg)

	for _, a := range reg.All() {
		if a.ParamsSchema == nil {
			t.Errorf("action %q : ParamsSchema nil (validation impossible)", a.ID)
		}
	}
}
