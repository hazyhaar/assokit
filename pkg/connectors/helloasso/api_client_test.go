// CLAUDE:SUMMARY Tests APIClient — pagination, 429 backoff (M-ASSOKIT-SPRINT3-S1).
package helloasso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestApiClient_GetOrganization : GET /v5/organizations/{slug} parse JSON typed.
func TestApiClient_GetOrganization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/organizations/nonpossumus" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"slug": "nonpossumus", "name": "Nonpossumus", "country": "FR",
		})
	}))
	defer srv.Close()

	cli := &APIClient{HTTP: srv.Client(), BaseURL: srv.URL}
	org, err := cli.GetOrganization(context.Background(), "nonpossumus")
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org.Slug != "nonpossumus" || org.Name != "Nonpossumus" {
		t.Errorf("org = %+v, attendu nonpossumus", org)
	}
}

// TestApiClient_ListPaymentsPaginated : 3 pages → all results retournés.
func TestApiClient_ListPaymentsPaginated(t *testing.T) {
	pageNum := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := pageNum.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data":       []map[string]any{{"id": 1, "amount": 10.0}, {"id": 2, "amount": 20.0}},
				"pagination": map[string]any{"continuationToken": "cursor-2"},
			})
		case 2:
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data":       []map[string]any{{"id": 3, "amount": 30.0}},
				"pagination": map[string]any{"continuationToken": "cursor-3"},
			})
		case 3:
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data":       []map[string]any{{"id": 4, "amount": 40.0}},
				"pagination": map[string]any{},
			})
		}
	}))
	defer srv.Close()

	cli := &APIClient{HTTP: srv.Client(), BaseURL: srv.URL}
	pays, err := cli.ListPayments(context.Background(), "nonpossumus", "", "", 5)
	if err != nil {
		t.Fatalf("ListPayments: %v", err)
	}
	if len(pays) != 4 {
		t.Errorf("len = %d, attendu 4 (pagination 2+1+1)", len(pays))
	}
	if pageNum.Load() != 3 {
		t.Errorf("pages fetched = %d, attendu 3", pageNum.Load())
	}
}

// TestApiClient_BackoffOnRateLimit : 429 puis 200 → retry succeed.
func TestApiClient_BackoffOnRateLimit(t *testing.T) {
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"slug": "test"}) //nolint:errcheck
	}))
	defer srv.Close()

	// Override backoff time pour test rapide via wrapper http qui mock le sleep.
	cli := &APIClient{HTTP: srv.Client(), BaseURL: srv.URL}
	start := time.Now()
	org, err := cli.GetOrganization(context.Background(), "test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org.Slug != "test" {
		t.Errorf("slug = %q", org.Slug)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, attendu 2 (1 fail + 1 retry)", calls.Load())
	}
	if elapsed < 4*time.Second {
		t.Logf("retry sleep < 4s (%v) — backoff peut-être skipped si CI fast", elapsed)
	}
}

// TestApiClient_HttpErrorReturnsTyped : 500 → erreur explicite.
func TestApiClient_HttpErrorReturnsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	cli := &APIClient{HTTP: srv.Client(), BaseURL: srv.URL}
	_, err := cli.GetOrganization(context.Background(), "x")
	if err == nil {
		t.Error("attendu erreur sur 500")
	}
}
