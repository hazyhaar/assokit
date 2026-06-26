package actions

import "context"

// mcpUmbrellaScope est le scope parapluie qui autorise l'usage de tous les outils
// MCP que le RBAC du compte permet. Les scopes fins homonymes d'une action
// (ex. "feedback.create") autorisent cette seule action.
const mcpUmbrellaScope = "mcp"

type scopesCtxKey struct{}

// ContextWithScopes attache les scopes OAuth du token Bearer courant. Posé par le
// middleware d'authentification MCP ; sa PRÉSENCE marque une requête passée par
// OAuth (par opposition à un appelant interne de confiance), ce qui déclenche
// l'application des scopes dans invokeMCPAction.
func ContextWithScopes(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, scopesCtxKey{}, scopes)
}

// ScopesFromContext retourne les scopes OAuth et un booléen indiquant si le
// contexte provient d'une requête OAuth (clé présente). Le booléen distingue
// "aucun scope consenti" (slice vide, viaOAuth=true → on applique) de "pas de
// contexte OAuth du tout" (viaOAuth=false → appelant de confiance, on n'applique pas).
func ScopesFromContext(ctx context.Context) ([]string, bool) {
	s, ok := ctx.Value(scopesCtxKey{}).([]string)
	return s, ok
}

// ScopeAllowsAction : un appel d'outil MCP est autorisé si le token porte le scope
// parapluie "mcp" ou le scope fin homonyme de l'action. Sans cela, le consentement
// OAuth est sans effet (least-privilege par scope non appliqué).
func ScopeAllowsAction(scopes []string, actionID string) bool {
	for _, s := range scopes {
		if s == mcpUmbrellaScope || s == actionID {
			return true
		}
	}
	return false
}
