package actions

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Action est l'unité atomique d'opération — source de vérité unique pour HTTP admin + MCP tool + OpenAPI.
type Action struct {
	ID           string            // ex. "forum.post.create"
	Title        string            // libellé UI
	Description  string            // pour LLM + tooltip admin
	ParamsSchema *jsonschema.Schema // validation des paramètres
	RequiredPerm string            // permission RBAC atomique
	Run          func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (Result, error)
}

// Result est le résultat d'une action.
type Result struct {
	Status  string // "ok"|"error"|"partial"
	Message string // message user-facing
	Data    any    // données sérialisables JSON
}
