package seeds

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/convention"
	"github.com/hazyhaar/assokit/pkg/gdpr"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/messaging"
	"github.com/hazyhaar/assokit/pkg/parcelle"
)

// initGDPR enregistre les actions de la couche RGPD transverse. Passer par le
// registry leur donne route HTTP admin + outil MCP + permission RBAC (LLM-parité) :
//   - gdpr.access_log_list : redevabilité d'accès, réservée aux administrateurs ;
//   - account.data_export  : portabilité ; un membre n'exporte QUE ses données.
func initGDPR(reg *actions.Registry) {
	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "gdpr.access_log_list",
		Title:        "Journal des accès nominatifs (RGPD)",
		Description:  "Retourne les accès aux données personnelles, à des fins de redevabilité. Filtrable par sujet (subject_kind+subject_id) ou par acteur (actor_id) ; sans filtre, retourne les accès les plus récents. Réservé aux administrateurs.",
		RequiredPerm: "gdpr.access_log_list",
		ParamsSchema: actions.MustSchema(`{
			"type":"object",
			"properties":{
				"subject_kind":{"type":"string"},
				"subject_id":{"type":"string"},
				"actor_id":{"type":"string"},
				"limit":{"type":"integer","minimum":1,"maximum":1000}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				SubjectKind string `json:"subject_kind"`
				SubjectID   string `json:"subject_id"`
				ActorID     string `json:"actor_id"`
				Limit       int    `json:"limit"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &gdpr.Store{DB: deps.DB}
			var (
				logs []gdpr.AccessLog
				err  error
			)
			switch {
			case p.SubjectKind != "" && p.SubjectID != "":
				logs, err = store.ListBySubject(ctx, p.SubjectKind, p.SubjectID)
			case p.ActorID != "":
				logs, err = store.ListForActor(ctx, p.ActorID)
			default:
				logs, err = store.ListRecent(ctx, p.Limit)
			}
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			return actions.Result{Status: "ok", Message: "ok", Data: logs}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "account.data_export",
		Title:        "Exporter mes données personnelles (RGPD)",
		Description:  "Restitue en JSON les données nominatives du membre courant : ses parcelles, ses droits cadastraux et ses messages privés. Un membre n'exporte QUE ses propres données ; l'export est lui-même journalisé.",
		RequiredPerm: "account.data_export",
		ParamsSchema: actions.MustSchema(`{"type":"object","properties":{}}`),
		Run: func(ctx context.Context, deps app.AppDeps, _ json.RawMessage) (actions.Result, error) {
			u := middleware.UserFromContext(ctx)
			if u == nil {
				return actions.Result{Status: "error", Message: "authentification requise"}, nil
			}
			data, err := ExportMemberData(ctx, deps, u.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			gdpr.LogAccess(ctx, &gdpr.Store{DB: deps.DB}, deps.Logger, gdpr.AccessLog{
				UserID:      u.ID,
				SubjectKind: gdpr.SubjectUser,
				SubjectID:   u.ID,
				ActorID:     u.ID,
				Action:      gdpr.ActionExport,
			})
			return actions.Result{Status: "ok", Message: "Données exportées.", Data: data}, nil
		},
	})
}

// MemberDataExport agrège les données nominatives d'un membre pour l'export RGPD.
// Les données de convention (module C4 à venir) s'ajouteront ici comme un champ
// supplémentaire dès que ce module écrira des données — la structure est conçue
// pour cette extension sans rupture du contrat JSON existant.
type MemberDataExport struct {
	UserID      string                  `json:"user_id"`
	Parcelles   []parcelle.Parcelle     `json:"parcelles"`
	Droits      []parcelle.Droit        `json:"droits"`
	Conventions []convention.Convention `json:"conventions"`
	Messages    []messaging.Message     `json:"messages"`
}

// ExportMemberData lit, en lecture seule, les données nominatives du membre :
// parcelles sur lesquelles il détient un droit, droits eux-mêmes, et messages
// échangés. Aucune donnée d'un autre membre n'est restituée. Exportée pour le
// handler HTTP /account/data-download, qui partage la même logique que l'action
// MCP account.data_export (parité).
func ExportMemberData(ctx context.Context, deps app.AppDeps, userID string) (MemberDataExport, error) {
	out := MemberDataExport{UserID: userID, Parcelles: []parcelle.Parcelle{}, Droits: []parcelle.Droit{}, Conventions: []convention.Convention{}, Messages: []messaging.Message{}}

	pStore := &parcelle.Store{DB: deps.DB}
	parcs, err := pStore.ListForUser(ctx, userID)
	if err != nil {
		return out, fmt.Errorf("account.data_export: parcelles: %w", err)
	}
	if parcs != nil {
		out.Parcelles = parcs
	}

	convs, err := (&convention.Store{DB: deps.DB}).ListForUser(ctx, userID)
	if err != nil {
		return out, fmt.Errorf("account.data_export: conventions: %w", err)
	}
	if convs != nil {
		out.Conventions = convs
	}

	droits, err := listDroitsForUser(ctx, deps, userID)
	if err != nil {
		return out, fmt.Errorf("account.data_export: droits: %w", err)
	}
	out.Droits = droits

	msgs, err := listMessagesForUser(ctx, deps, userID)
	if err != nil {
		return out, fmt.Errorf("account.data_export: messages: %w", err)
	}
	out.Messages = msgs

	return out, nil
}

// listDroitsForUser retourne les droits cadastraux d'un membre. Lecture seule
// directe : le module parcelle n'expose pas de lister de droits par membre, et
// recoder un Store entier serait une duplication ; cette requête restreinte est
// locale à l'export.
func listDroitsForUser(ctx context.Context, deps app.AppDeps, userID string) ([]parcelle.Droit, error) {
	rows, err := deps.DB.QueryContext(ctx, `
		SELECT id, parcelle_id, user_id, nature_du_droit, created_at
		FROM parcelle_droits WHERE user_id = ?
		ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []parcelle.Droit{}
	for rows.Next() {
		var d parcelle.Droit
		var created string
		if err := rows.Scan(&d.ID, &d.ParcelleID, &d.UserID, &d.NatureDuDroit, &created); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// listMessagesForUser retourne tous les messages où le membre est expéditeur ou
// destinataire (ses propres échanges, jamais ceux d'autrui).
func listMessagesForUser(ctx context.Context, deps app.AppDeps, userID string) ([]messaging.Message, error) {
	rows, err := deps.DB.QueryContext(ctx, `
		SELECT id, sender_id, recipient_id, body_md, body_html, created_at
		FROM messages WHERE sender_id = ? OR recipient_id = ?
		ORDER BY created_at
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []messaging.Message{}
	for rows.Next() {
		var m messaging.Message
		var created string
		if err := rows.Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.BodyMD, &m.BodyHTML, &created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
