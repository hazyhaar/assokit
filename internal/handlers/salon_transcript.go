// CLAUDE:SUMMARY Handlers T5 — comptes-rendus par salon : liste, détail segments, téléchargement (.txt).
// Gardes : authentification + IsMember + vérification salon_id (404 cross-salon).
package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/salon"
	"github.com/hazyhaar/assokit/pkg/transcript"
)

// MountTranscriptRoutes câble les routes comptes-rendus dans MountSalonRoutes.
// Appelé depuis salon.go après les routes existantes.
func MountTranscriptRoutes(r chi.Router, deps app.AppDeps) {
	r.Get("/salon/{slug}/transcripts", handleTranscriptList(deps))
	r.Get("/salon/{slug}/transcripts/{id}", handleTranscriptDetail(deps))
	r.Get("/salon/{slug}/transcripts/{id}/download", handleTranscriptDownload(deps))
}

// transcriptStatusLabel traduit le code de statut machine en libellé français clair.
func transcriptStatusLabel(status string) string {
	switch status {
	case "pending":
		return "En attente"
	case "recording":
		return "En cours d'enregistrement"
	case "transcribing":
		return "Transcription en cours"
	case "done":
		return "Terminé"
	case "failed":
		return "Échec"
	default:
		return status
	}
}

// transcriptDuration retourne une durée lisible ou une chaîne vide si ended_at absent.
func transcriptDuration(tr *transcript.Transcript) string {
	if !tr.EndedAt.Valid {
		return ""
	}
	d := tr.EndedAt.Time.Sub(tr.StartedAt).Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// requireSalonMember est une garde commune : authentification + IsMember.
// Retourne le salon et false si une réponse d'erreur a déjà été écrite.
func requireSalonMember(w http.ResponseWriter, r *http.Request, deps app.AppDeps, slug string) (*salon.Salon, bool) {
	u := middleware.UserFromContext(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return nil, false
	}
	st := &salon.Store{DB: deps.DB}
	sl, err := st.Get(r.Context(), slug)
	if err != nil {
		http.Error(w, "Salon introuvable", http.StatusNotFound)
		return nil, false
	}
	ok, err := st.IsMember(r.Context(), slug, u.ID)
	if err != nil {
		deps.Logger.Warn("transcript_member_check", "slug", slug, "err", err.Error())
		http.Error(w, "Erreur interne", http.StatusInternalServerError)
		return nil, false
	}
	if !ok {
		http.Error(w, "Accès réservé aux membres du salon", http.StatusForbidden)
		return nil, false
	}
	return sl, true
}

// handleTranscriptList affiche la liste des comptes-rendus d'un salon.
func handleTranscriptList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		sl, ok := requireSalonMember(w, r, deps, slug)
		if !ok {
			return
		}
		ts := &transcript.Store{DB: deps.DB}
		list, err := ts.ListBySalon(r.Context(), sl.ID)
		if err != nil {
			deps.Logger.Error("transcript_list", "slug", slug, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		rows := make([]views.TranscriptRow, len(list))
		for i, t := range list {
			rows[i] = views.TranscriptRow{T: t, Duration: transcriptDuration(t), Status: transcriptStatusLabel(t.Status)}
		}
		renderPageV2(w, r, deps, "Comptes-rendus — "+sl.Name, views.TranscriptList(sl, rows))
	}
}

// handleTranscriptDetail affiche le détail d'un compte-rendu avec ses segments.
func handleTranscriptDetail(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		id := chi.URLParam(r, "id")
		sl, ok := requireSalonMember(w, r, deps, slug)
		if !ok {
			return
		}
		ts := &transcript.Store{DB: deps.DB}
		tr, err := ts.GetTranscript(r.Context(), id)
		if errors.Is(err, transcript.ErrNotFound) || (err == nil && tr.SalonID != sl.ID) {
			http.Error(w, "Compte-rendu introuvable", http.StatusNotFound)
			return
		}
		if err != nil {
			deps.Logger.Error("transcript_get", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		segs, err := ts.Segments(r.Context(), id)
		if err != nil {
			deps.Logger.Error("transcript_segments", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Compte-rendu du "+tr.StartedAt.Format("02/01/2006"), views.TranscriptDetail(sl, tr, segs, transcriptStatusLabel(tr.Status)))
	}
}

// handleTranscriptDownload retourne le compte-rendu en texte brut (.txt).
func handleTranscriptDownload(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		id := chi.URLParam(r, "id")
		sl, ok := requireSalonMember(w, r, deps, slug)
		if !ok {
			return
		}
		ts := &transcript.Store{DB: deps.DB}
		tr, err := ts.GetTranscript(r.Context(), id)
		if errors.Is(err, transcript.ErrNotFound) || (err == nil && tr.SalonID != sl.ID) {
			http.Error(w, "Compte-rendu introuvable", http.StatusNotFound)
			return
		}
		if err != nil {
			deps.Logger.Error("transcript_download_get", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		segs, err := ts.Segments(r.Context(), id)
		if err != nil {
			deps.Logger.Error("transcript_download_segments", "id", id, "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}

		filename := fmt.Sprintf("compte-rendu-%s-%s.txt", sl.Slug, tr.StartedAt.Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		var b strings.Builder
		b.WriteString("Salon : " + sl.Name + "\n")
		b.WriteString("Date  : " + tr.StartedAt.Format("02/01/2006 15:04") + "\n")
		if tr.EndedAt.Valid {
			b.WriteString("Durée : " + transcriptDuration(tr) + "\n")
		}
		b.WriteString("\n")
		for _, seg := range segs {
			b.WriteString("[" + seg.Speaker + "] " + seg.Text + "\n")
		}
		_, _ = w.Write([]byte(b.String()))
	}
}
