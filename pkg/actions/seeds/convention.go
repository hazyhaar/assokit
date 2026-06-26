package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// initConvention enregistre les actions du module convention de pâturage (C4).
// Passer par le registry leur donne route HTTP admin + outil MCP + permission RBAC
// (parité). Le contenu des clauses reste produit hors du core : convention.generate
// assemble la convention via le fournisseur injecté (convention.ClauseProvider),
// inerte par défaut.
//   - convention.create   : crée une convention (preneur + parcelles + durée) ;
//   - convention.generate : assemble la convention et ses clauses (lecture) ;
//   - convention.revoke   : passe une convention au statut révoqué.
func initConvention(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "convention.create",
		Title:        "Créer une convention de pâturage",
		Description:  "Crée une convention de pâturage rattachant un membre preneur à une ou plusieurs parcelles, pour une durée en mois. Réservé aux administrateurs. Le contenu des clauses n'est pas stocké : il est assemblé à la génération.",
		RequiredPerm: "convention.create",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"preneur_id":{"type":"string"},
				"parcelles":{"type":"array","items":{"type":"string"}},
				"duree_mois":{"type":"integer","minimum":1}
			},
			"required":["preneur_id","parcelles"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				PreneurID string   `json:"preneur_id"`
				Parcelles []string `json:"parcelles"`
				DureeMois int      `json:"duree_mois"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &convention.Store{DB: deps.DB}
			id, err := store.Create(ctx, convention.Convention{
				PreneurID: p.PreneurID,
				Parcelles: p.Parcelles,
				DureeMois: p.DureeMois,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			logActorAccess(ctx, deps, gdpr.SubjectUser, p.PreneurID, gdpr.ActionView)
			return actions.Result{Status: "ok", Message: "Convention créée.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "convention.generate",
		Title:        "Assembler une convention de pâturage",
		Description:  "Assemble une convention existante et ses clauses, via le fournisseur de clauses injecté à la bordure. Sans fournisseur réel branché, la convention est retournée sans clause (état vide assumé). Réservé aux administrateurs.",
		RequiredPerm: "convention.generate",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"id":{"type":"string"}},
			"required":["id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			conv, clauses, err := AssembleConvention(ctx, deps, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			logActorAccess(ctx, deps, gdpr.SubjectUser, conv.PreneurID, gdpr.ActionView)
			return actions.Result{
				Status:  "ok",
				Message: "Convention assemblée.",
				Data:    map[string]any{"convention": conv, "clauses": clauses},
			}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "convention.revoke",
		Title:        "Révoquer une convention de pâturage",
		Description:  "Passe une convention existante au statut révoqué. La convention est conservée (trace), rien n'est supprimé. Réservé aux administrateurs.",
		RequiredPerm: "convention.revoke",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"id":{"type":"string"}},
			"required":["id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &convention.Store{DB: deps.DB}
			if err := store.Revoke(ctx, p.ID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Convention révoquée."}, nil
		},
	})
}

// AssembleConvention charge une convention et demande ses clauses au fournisseur
// injecté. Logique partagée par l'action MCP convention.generate et le handler
// HTTP de génération (parité). Le fournisseur par défaut (InertProvider) retourne
// une liste vide : aucune clause n'est inventée.
func AssembleConvention(ctx context.Context, deps app.AppDeps, id string) (convention.Convention, []convention.Clause, error) {
	conv, err := (&convention.Store{DB: deps.DB}).GetByID(ctx, id)
	if err != nil {
		return conv, nil, err
	}
	provider := deps.ConventionProvider
	if provider == nil {
		provider = convention.InertProvider{}
	}
	clauses, err := provider.Clauses(ctx, convention.ConventionContext{
		PreneurID:      conv.PreneurID,
		Parcelles:      conv.Parcelles,
		DureeMois:      conv.DureeMois,
		UsagePrincipal: "pastoral",
	})
	if err != nil {
		return conv, nil, err
	}
	return conv, clauses, nil
}

// logActorAccess journalise un accès nominatif effectué par l'acteur courant
// (depuis le contexte MCP). N'échoue jamais l'action observée.
func logActorAccess(ctx context.Context, deps app.AppDeps, subjectKind, subjectID, action string) {
	u := middleware.UserFromContext(ctx)
	if u == nil {
		return
	}
	gdpr.LogAccess(ctx, &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
		UserID:      subjectID,
		SubjectKind: subjectKind,
		SubjectID:   subjectID,
		ActorID:     u.ID,
		Action:      action,
	})
}
