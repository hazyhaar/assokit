package actions

import (
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// MustSchema compile un schema JSON inline. Panic si invalide (utilisé uniquement pour les seeds statiques).
func MustSchema(raw string) *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	c.AddResource("schema.json", strings.NewReader(raw))
	s, err := c.Compile("schema.json")
	if err != nil {
		panic("actions.MustSchema: " + err.Error())
	}
	return s
}
