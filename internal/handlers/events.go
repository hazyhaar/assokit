// CLAUDE:SUMMARY Handlers UI agenda (vague 2 : renderPageV2 + views.Events*).
package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/webui/views"
	"github.com/hazyhaar/assokit/pkg/horui/events"
)

// handleEventsList affiche la liste publique des événements (à venir d'abord).
func handleEventsList(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := &events.Store{DB: deps.DB}
		list, err := store.List(r.Context())
		if err != nil {
			deps.Logger.Error("events_list", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, "Agenda", views.EventsList(list))
	}
}

// handleEventDetail affiche la fiche d'un événement par son slug.
func handleEventDetail(deps app.AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		store := &events.Store{DB: deps.DB}
		e, err := store.Get(r.Context(), slug)
		if errors.Is(err, events.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			deps.Logger.Error("event_detail", "err", err.Error())
			http.Error(w, "Erreur interne", http.StatusInternalServerError)
			return
		}
		renderPageV2(w, r, deps, e.Title, views.EventDetail(e))
	}
}
