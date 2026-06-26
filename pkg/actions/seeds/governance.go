package seeds

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/governance"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/identity"
	"github.com/hazyhaar/assokit/pkg/membership"
)

// initGovernance enregistre les actions du socle réunion de gouvernance (V2a).
// Passer par le registry leur donne route HTTP admin + outil MCP + permission RBAC
// (parité). Aucun mandat, émargement, quorum, vote ni procès-verbal à ce stade.
//   - governance.create_assembly : crée une assemblée générale en brouillon ;
//   - governance.convoke         : convoque l'assemblée et courrielle les adhérents actifs.
func initGovernance(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.create_assembly",
		Title:        "Créer une assemblée générale",
		Description:  "Crée une assemblée générale en brouillon (nom, ordre du jour, date programmée). Réservé aux administrateurs. La convocation des adhérents est une étape distincte.",
		RequiredPerm: "governance.create_assembly",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"name":{"type":"string"},
				"agenda":{"type":"string"},
				"scheduled_at":{"type":"string"}
			},
			"required":["name"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				Name        string `json:"name"`
				Agenda      string `json:"agenda"`
				ScheduledAt string `json:"scheduled_at"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			id, err := store.Create(ctx, governance.Assembly{
				Name:        p.Name,
				Agenda:      p.Agenda,
				ScheduledAt: p.ScheduledAt,
			})
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Assemblée générale créée.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.convoke",
		Title:        "Convoquer une assemblée générale",
		Description:  "Passe une assemblée générale du statut brouillon à convoquée et envoie un courriel de convocation à chaque adhérent actif. L'accès nominatif aux adhérents est journalisé. Sans adhérent actif, aucun courriel n'est envoyé (état vide assumé). Réservé aux administrateurs.",
		RequiredPerm: "governance.convoke",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"assembly_id":{"type":"string"}},
			"required":["assembly_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID string `json:"assembly_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			n, err := Convoke(ctx, deps, p.AssemblyID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{
				Status:  "ok",
				Message: fmt.Sprintf("Assemblée convoquée, %d courriel(s) en file.", n),
				Data:    map[string]int{"convocations": n},
			}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.record_mandate",
		Title:        "Enregistrer un pouvoir de représentation",
		Description:  "Enregistre un mandat : un adhérent actif (mandant) confie sa représentation à un autre membre (mandataire) pour une assemblée. Rejeté si mandant et mandataire sont identiques, si un mandat existe déjà pour ce mandant sur cette assemblée, ou si le mandant n'est pas adhérent actif. L'accès nominatif est journalisé. Réservé aux administrateurs.",
		RequiredPerm: "governance.record_mandate",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"assembly_id":{"type":"string"},
				"principal_user_id":{"type":"string"},
				"proxy_user_id":{"type":"string"}
			},
			"required":["assembly_id","principal_user_id","proxy_user_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID  string `json:"assembly_id"`
				PrincipalID string `json:"principal_user_id"`
				ProxyID     string `json:"proxy_user_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			members := &membership.Store{DB: deps.DB}
			id, err := store.RecordMandate(ctx, members, p.AssemblyID, p.PrincipalID, p.ProxyID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			// Accès nominatif (mandant et mandataire désignés) lors de la pose du mandat.
			logActorAccessOn(ctx, deps, gdpr.SubjectAssembly, p.PrincipalID, gdpr.ActionView)
			logActorAccessOn(ctx, deps, gdpr.SubjectAssembly, p.ProxyID, gdpr.ActionView)
			return actions.Result{Status: "ok", Message: "Mandat enregistré.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.sign_attendance",
		Title:        "Émarger un membre",
		Description:  "Marque un membre présent à une assemblée (émargement). Opération idempotente : émarger deux fois le même membre ne crée pas de doublon. Réservé aux administrateurs.",
		RequiredPerm: "governance.sign_attendance",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"assembly_id":{"type":"string"},
				"member_user_id":{"type":"string"},
				"method":{"type":"string"}
			},
			"required":["assembly_id","member_user_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID   string `json:"assembly_id"`
				MemberUserID string `json:"member_user_id"`
				Method       string `json:"method"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			id, err := store.SignAttendance(ctx, p.AssemblyID, p.MemberUserID, p.Method)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			logActorAccessOn(ctx, deps, gdpr.SubjectAssembly, p.MemberUserID, gdpr.ActionView)
			return actions.Result{Status: "ok", Message: "Émargement enregistré.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.check_quorum",
		Title:        "Calculer le quorum d'une assemblée",
		Description:  "Calcule le quorum d'une assemblée : population éligible (adhérents actifs), présents émargés, mandants représentés, total présent ou représenté (sans double comptage) et atteinte du seuil. Le seuil est paramétrable (défaut 0,5). Réservé aux administrateurs.",
		RequiredPerm: "governance.check_quorum",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"assembly_id":{"type":"string"},
				"threshold":{"type":"number"}
			},
			"required":["assembly_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID string   `json:"assembly_id"`
				Threshold  *float64 `json:"threshold"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			threshold := 0.5
			if p.Threshold != nil {
				threshold = *p.Threshold
			}
			result, err := CheckQuorum(ctx, deps, p.AssemblyID, threshold)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Quorum calculé.", Data: result}, nil
		},
	})

	initGovernanceVote(reg)
	initGovernanceMinutes(reg)
}

// initGovernanceMinutes enregistre les actions de trace : procès-verbal et registre
// append-only (V2d) :
//   - governance.record_deliberation : consigne au registre le dépouillement figé d'une
//     résolution close (mutation INSERT, immuable) ;
//   - governance.generate_minutes    : génère le procès-verbal d'une AG convoquée
//     (mutation INSERT, immuable) ;
//   - governance.list_register       : liste les délibérations consignées (lecture seule).
func initGovernanceMinutes(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.record_deliberation",
		Title:        "Consigner une délibération au registre",
		Description:  "Consigne au registre des délibérations le dépouillement figé d'une résolution close (pour, contre, abstention, total copiés au moment de la consignation). Rejeté si le scrutin n'est pas clos ou si la résolution est déjà consignée (une seule consignation par résolution, jamais d'écrasement). L'entrée de registre est immuable. Réservé aux administrateurs.",
		RequiredPerm: "governance.record_deliberation",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"resolution_id":{"type":"string"}},
			"required":["resolution_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ResolutionID string `json:"resolution_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			id, err := store.RecordDeliberation(ctx, p.ResolutionID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Délibération consignée au registre.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.generate_minutes",
		Title:        "Générer le procès-verbal",
		Description:  "Génère le procès-verbal d'une assemblée convoquée : un instantané texte du nom, de l'ordre du jour et des délibérations consignées avec leurs dépouillements. Rejeté si l'assemblée n'est pas convoquée, si au moins une résolution close n'a pas encore été consignée au registre (un PV immuable ne fige jamais un registre incomplet), ou si un procès-verbal existe déjà (un seul PV par assemblée, immuable, jamais d'écrasement). Le procès-verbal ne consigne que des décomptes agrégés et des libellés, sans accès nominatif individuel : aucun journal d'accès nominatif n'est requis. Réserve : l'ordre du jour est un texte libre pouvant mentionner une personne ; ce risque nominatif éventuel dépend du contenu saisi, non de la structure du procès-verbal. Réservé aux administrateurs.",
		RequiredPerm: "governance.generate_minutes",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"assembly_id":{"type":"string"}},
			"required":["assembly_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID string `json:"assembly_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			id, err := store.GeneratePV(ctx, p.AssemblyID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Procès-verbal généré.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.list_register",
		Title:        "Lister le registre des délibérations",
		Description:  "Liste les délibérations consignées d'une assemblée (libellé et dépouillement figé de chaque résolution close consignée), dans l'ordre de consignation. Lecture seule. Réservé aux administrateurs.",
		RequiredPerm: "governance.list_register",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"assembly_id":{"type":"string"}},
			"required":["assembly_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID string `json:"assembly_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			register, err := store.ListRegister(ctx, p.AssemblyID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Registre des délibérations.", Data: register}, nil
		},
	})
}

// initGovernanceVote enregistre les actions de décision : vote simple (V2c) :
//   - governance.open_resolution  : ouvre une question au vote (AG convoquée) ;
//   - governance.cast_vote        : enregistre un bulletin (4 gardes, éligibilité du votant) ;
//   - governance.close_resolution : clôt le scrutin (open→closed) ;
//   - governance.tally_resolution : dépouille (lecture seule).
func initGovernanceVote(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.open_resolution",
		Title:        "Ouvrir une résolution au vote",
		Description:  "Ouvre une question (résolution) au vote sur une assemblée convoquée. La résolution est créée au statut ouvert ; les bulletins ne sont acceptés que tant que le scrutin est ouvert. Réservé aux administrateurs.",
		RequiredPerm: "governance.open_resolution",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"assembly_id":{"type":"string"},
				"label":{"type":"string"}
			},
			"required":["assembly_id","label"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				AssemblyID string `json:"assembly_id"`
				Label      string `json:"label"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			id, err := store.OpenResolution(ctx, p.AssemblyID, p.Label)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Résolution ouverte au vote.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.cast_vote",
		Title:        "Enregistrer un vote",
		Description:  "Enregistre le bulletin d'un votant sur une résolution ouverte. Le choix est l'un de pour, contre ou abstention. Rejeté si le scrutin est clos, si le choix est invalide, si le votant n'est pas adhérent actif, ou si le votant a déjà voté (un seul bulletin par votant et par résolution, jamais d'écrasement). L'accès nominatif au vote est journalisé. Réservé aux administrateurs.",
		RequiredPerm: "governance.cast_vote",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"resolution_id":{"type":"string"},
				"voter_user_id":{"type":"string"},
				"choice":{"type":"string","enum":["pour","contre","abstention"]}
			},
			"required":["resolution_id","voter_user_id","choice"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ResolutionID string `json:"resolution_id"`
				VoterUserID  string `json:"voter_user_id"`
				Choice       string `json:"choice"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			members := &membership.Store{DB: deps.DB}
			id, err := store.CastVoteWithChecker(ctx, members, p.ResolutionID, p.VoterUserID, p.Choice)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			// Accès nominatif (bulletin d'un votant désigné) lors de l'enregistrement.
			logActorAccessOn(ctx, deps, gdpr.SubjectAssembly, p.VoterUserID, gdpr.ActionView)
			return actions.Result{Status: "ok", Message: "Vote enregistré.", Data: map[string]string{"id": id}}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.close_resolution",
		Title:        "Clôturer une résolution",
		Description:  "Clôt le scrutin d'une résolution (transition ouvert→clos). Aucun bulletin n'est plus accepté ensuite. Rejeté si la résolution est déjà close ou inexistante. Réservé aux administrateurs.",
		RequiredPerm: "governance.close_resolution",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"resolution_id":{"type":"string"}},
			"required":["resolution_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ResolutionID string `json:"resolution_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			if err := store.CloseResolution(ctx, p.ResolutionID); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Scrutin clos."}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "governance.tally_resolution",
		Title:        "Dépouiller une résolution",
		Description:  "Dépouille une résolution : décompte des bulletins par choix (pour, contre, abstention) et total des votes enregistrés. Lecture seule. Réservé aux administrateurs.",
		RequiredPerm: "governance.tally_resolution",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{"resolution_id":{"type":"string"}},
			"required":["resolution_id"]
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ResolutionID string `json:"resolution_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &governance.Store{DB: deps.DB}
			t, err := store.Tally(ctx, p.ResolutionID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "Dépouillement calculé.", Data: t}, nil
		},
	})
}

// CheckQuorum résout la population éligible d'une assemblée (adhérents actifs réels,
// via membership.Store) PUIS délègue le décompte à governance.ComputeQuorum, qui
// reste agnostique de la règle d'adhésion. Ce découpage place la connaissance « qui
// est adhérent actif » au niveau du handler/action (consommateur), pas dans le module
// gouvernance pur. Logique partagée par l'action MCP governance.check_quorum et le
// handler HTTP de quorum (parité). Le seuil est un paramètre (défaut 0,5 côté action).
func CheckQuorum(ctx context.Context, deps app.AppDeps, assemblyID string, threshold float64) (governance.QuorumResult, error) {
	gov := &governance.Store{DB: deps.DB}
	if _, err := gov.GetByID(ctx, assemblyID); err != nil {
		return governance.QuorumResult{}, err // ErrAssemblyNotFound diagnostiqué ici.
	}
	// Adhérents actifs réels (jamais fabriqués), dédupliqués par membre : la
	// population éligible de l'assemblée. Calculée ici (consommateur), pas dans le
	// module gouvernance, qui la reçoit en argument.
	memberships, err := (&membership.Store{DB: deps.DB}).ListAll(ctx, "active")
	if err != nil {
		return governance.QuorumResult{}, err
	}
	eligibleIDs := dedupActiveUserIDs(memberships)
	return gov.ComputeQuorum(ctx, assemblyID, eligibleIDs, threshold)
}

// Convoke convoque une assemblée générale : transition brouillon→convoquée, puis,
// pour chaque adhérent actif réel, mise en file d'un courriel de convocation
// horodaté et enregistrement de la trace. Logique partagée par l'action MCP
// governance.convoke et le handler HTTP de convocation (parité). Retourne le nombre
// de convocations envoyées. Aucun adhérent actif → 0 courriel, sans erreur.
func Convoke(ctx context.Context, deps app.AppDeps, assemblyID string) (int, error) {
	gov := &governance.Store{DB: deps.DB}
	asm, err := gov.GetByID(ctx, assemblyID)
	if err != nil {
		return 0, err // ErrAssemblyNotFound diagnostiqué ici, avant toute écriture.
	}

	// Adhérents actifs réels (jamais fabriqués) : les adhésions au statut actif
	// désignent les destinataires. Un même membre peut porter plusieurs adhésions
	// actives → déduplication par identifiant. Résolus AVANT la transaction pour ne
	// garder dans la section critique que les écritures.
	memberships, err := (&membership.Store{DB: deps.DB}).ListAll(ctx, "active")
	if err != nil {
		return 0, err
	}
	idStore := &identity.Store{DB: deps.DB}

	// destinataire = adhérent actif réel résolu en compte existant.
	type recipient struct {
		userID      string
		email       string
		displayName string
	}
	var recipients []recipient
	for _, userID := range dedupActiveUserIDs(memberships) {
		u, err := idStore.GetByID(ctx, userID)
		if err != nil {
			return 0, err
		}
		if u == nil {
			continue // adhésion orpheline (compte supprimé) : ignorée, pas fabriquée
		}
		recipients = append(recipients, recipient{userID: userID, email: u.Email, displayName: u.DisplayName})
	}

	// Section critique atomique (patron convention.Create) : la transition
	// drafting→convoked ET toutes les traces de convocation sont committées ensemble.
	// Invariant : « AG convoked ⟺ traces de convocation complètes ». Un échec avant
	// commit → rollback : l'AG reste drafting, zéro trace, état rattrapable.
	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("governance.Convoke begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := gov.MarkConvokedTx(ctx, tx, assemblyID); err != nil {
		return 0, err
	}
	for _, r := range recipients {
		if err := gov.RecordConvocationTx(ctx, tx, assemblyID, r.userID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("governance.Convoke commit: %w", err)
	}

	// APRÈS commit seulement : mise en file durable des courriels (best-effort). Le
	// mailer possède sa propre table email_outbox (file durable drainée par le
	// worker) ; l'INSERT email_outbox n'est PAS dupliqué dans la transaction
	// ci-dessus. Un échec d'Enqueue est journalisé sans invalider la convocation déjà
	// committée : la trace convocations permet une re-livraison ultérieure.
	subject := fmt.Sprintf("Convocation : %s", asm.Name)
	sentAt := time.Now().UTC().Format("2006-01-02 15:04")
	for _, r := range recipients {
		body := convocationBody(r.displayName, asm, sentAt)
		if deps.Mailer != nil {
			if err := deps.Mailer.Enqueue(ctx, r.email, subject, body, ""); err != nil {
				if deps.Logger != nil {
					deps.Logger.Warn("governance.Convoke: courriel non mis en file (re-livrable via trace convocations)",
						"assembly_id", assemblyID, "user_id", r.userID, "err", err)
				}
			}
		}
		// Accès nominatif à un adhérent lors de la convocation : journalisé.
		logActorAccessOn(ctx, deps, gdpr.SubjectAssembly, r.userID, gdpr.ActionView)
	}
	return len(recipients), nil
}

// convocationBody compose le corps texte du courriel de convocation. Mentionne le
// nom de l'assemblée, l'ordre du jour et la date programmée.
func convocationBody(displayName string, asm governance.Assembly, sentAt string) string {
	agenda := asm.Agenda
	if agenda == "" {
		agenda = "(à préciser)"
	}
	date := asm.ScheduledAt
	if date == "" {
		date = "(à préciser)"
	}
	// Corps en TEXTE BRUT (Content-Type text/plain ; Enqueue reçoit ce corps comme
	// textBody, htmlBody vide). Aucun échappement n'est requis ici. Tout passage
	// futur à un corps HTML imposerait d'échapper les champs interpolés (DisplayName,
	// asm.Name, agenda) pour éviter une injection de balises.
	return fmt.Sprintf(
		"Bonjour %s,\n\n"+
			"Une convocation est adressée pour l'assemblée générale « %s ».\n\n"+
			"Date : %s\n"+
			"Ordre du jour :\n%s\n\n"+
			"Convocation émise le %s.\n",
		displayName, asm.Name, date, agenda, sentAt,
	)
}

// dedupActiveUserIDs extrait les identifiants de membres distincts d'une liste
// d'adhésions, en préservant l'ordre.
func dedupActiveUserIDs(ms []membership.Membership) []string {
	seen := make(map[string]struct{}, len(ms))
	var out []string
	for _, m := range ms {
		if _, ok := seen[m.UserID]; ok {
			continue
		}
		seen[m.UserID] = struct{}{}
		out = append(out, m.UserID)
	}
	return out
}

// logActorAccessOn journalise un accès nominatif effectué par l'acteur courant. Le
// sujet (SubjectID) est le membre consulté ; l'acteur est tiré du contexte MCP. Si
// aucun acteur n'est dans le contexte (montage de test), aucun log n'est écrit (la
// route admin garantit l'acteur en prod). N'échoue jamais l'action observée.
func logActorAccessOn(ctx context.Context, deps app.AppDeps, subjectKind, subjectID, action string) {
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
