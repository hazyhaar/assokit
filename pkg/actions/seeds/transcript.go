package seeds

import (
	"context"
	"encoding/json"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/transcript"
)

// initTranscript enregistre les actions de lecture des transcripts.
// Les actions d'écriture (création, ajout de segment) ne sont pas exposées ici :
// elles appartiennent au bot transcripteur (T3), pas à l'utilisateur.
func initTranscript(reg *actions.Registry) {
	const perm = "salon.use"

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "transcript.list",
		Title:        "Lister les transcripts d'un salon",
		Description:  "Retourne les transcripts d'un salon, du plus récent au plus ancien.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["salon_id"],
			"properties":{
				"salon_id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				SalonID string `json:"salon_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &transcript.Store{DB: deps.DB}
			list, err := store.ListBySalon(ctx, p.SalonID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, tr := range list {
				row := map[string]any{
					"id":         tr.ID,
					"salon_id":   tr.SalonID,
					"started_at": tr.StartedAt.Format("2006-01-02 15:04:05"),
					"status":     tr.Status,
				}
				if tr.EndedAt.Valid {
					row["ended_at"] = tr.EndedAt.Time.Format("2006-01-02 15:04:05")
				}
				out = append(out, row)
			}
			return actions.Result{Status: "ok", Message: "ok", Data: out}, nil
		},
	})

	reg.Add(actions.Action{ //nolint:errcheck
		ID:           "transcript.get",
		Title:        "Lire un transcript avec ses segments",
		Description:  "Retourne le transcript et ses segments de parole triés par ordre chronologique.",
		RequiredPerm: perm,
		ParamsSchema: actions.MustSchema(`{
			"type":"object","required":["id"],
			"properties":{
				"id":{"type":"string","minLength":1}
			}
		}`),
		Run: func(ctx context.Context, deps app.AppDeps, params json.RawMessage) (actions.Result, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			store := &transcript.Store{DB: deps.DB}
			tr, err := store.GetTranscript(ctx, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, nil
			}
			segs, err := store.Segments(ctx, p.ID)
			if err != nil {
				return actions.Result{Status: "error", Message: err.Error()}, err
			}
			segOut := make([]map[string]any, 0, len(segs))
			for _, seg := range segs {
				segOut = append(segOut, map[string]any{
					"id":      seg.ID,
					"speaker": seg.Speaker,
					"t0_ms":   seg.T0ms,
					"t1_ms":   seg.T1ms,
					"text":    seg.Text,
				})
			}
			row := map[string]any{
				"id":         tr.ID,
				"salon_id":   tr.SalonID,
				"started_at": tr.StartedAt.Format("2006-01-02 15:04:05"),
				"status":     tr.Status,
				"segments":   segOut,
			}
			if tr.EndedAt.Valid {
				row["ended_at"] = tr.EndedAt.Time.Format("2006-01-02 15:04:05")
			}
			return actions.Result{Status: "ok", Message: "ok", Data: row}, nil
		},
	})
}
