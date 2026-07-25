package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/hazyhaar/assokit/pkg/listing"

	_ "modernc.org/sqlite"
)

func newTestHandlers(t *testing.T) *yachtsHandlers {
	t.Helper()
	silo, err := listing.LoadSilo("../../config/silos/yachts.toml")
	if err != nil {
		t.Fatalf("LoadSilo: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "yachts_test.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := listing.NewStore(context.Background(), db, silo)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := seedIfEmpty(context.Background(), store); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &yachtsHandlers{store: store, silo: silo}
}

func TestHandleIndex_RendersCards(t *testing.T) {
	h := newTestHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/annonces", nil)
	rec := httptest.NewRecorder()
	h.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "NEEL") {
		t.Errorf("index ne contient pas d'annonce réelle (NEEL)")
	}
	if !strings.Contains(body, "Marque") {
		t.Errorf("index ne contient pas la facette Marque")
	}
}

func TestHandleFragment_FacetFilter(t *testing.T) {
	h := newTestHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/annonces/fragment?marque=NEEL", nil)
	rec := httptest.NewRecorder()
	h.handleFragment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Bavaria") {
		t.Errorf("le filtre marque=NEEL laisse passer Bavaria")
	}
	if !strings.Contains(body, "NEEL") {
		t.Errorf("le filtre marque=NEEL ne renvoie aucun NEEL")
	}
}

func TestHandleDetail_ShowsSpecs(t *testing.T) {
	h := newTestHandlers(t)
	all, err := h.store.Search(context.Background(), listing.Filter{Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("aucune annonce seedée")
	}
	r := chi.NewRouter()
	r.Get("/annonces/{id}", h.handleDetail)
	req := httptest.NewRequest(http.MethodGet, "/annonces/"+all[0].ID, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Caractéristiques") {
		t.Errorf("détail ne contient pas le bloc Caractéristiques")
	}
}
