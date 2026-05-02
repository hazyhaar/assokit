// CLAUDE:SUMMARY API client typé HelloAsso v5 — GetOrganization, ListPayments, GetPayment, ListForms (M-ASSOKIT-SPRINT3-S1).
// CLAUDE:WARN ListPayments paginate via top-level "data"+"pagination" du JSON HelloAsso. Backoff 429 = sleep 5s + 1 retry.
package helloasso

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// APIClient wrap les calls HelloAsso v5 typés.
type APIClient struct {
	HTTP    *http.Client
	BaseURL string
}

// Organization : slim model des champs utiles.
type Organization struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Country string `json:"country,omitempty"`
}

// Payment : slim model d'un paiement HelloAsso.
type Payment struct {
	ID            int64   `json:"id"`
	Amount        float64 `json:"amount"`
	State         string  `json:"state"`
	Date          string  `json:"date"`
	PayerEmail    string  `json:"payerEmail,omitempty"`
	PayerName     string  `json:"payerName,omitempty"`
	FormSlug      string  `json:"formSlug,omitempty"`
	OrganizationSlug string `json:"organizationSlug,omitempty"`
}

// Form : slim model campagne HelloAsso (Don, Adhésion, Crowdfunding, etc.).
type Form struct {
	Slug      string `json:"formSlug"`
	Title     string `json:"title"`
	FormType  string `json:"formType"`
	State     string `json:"state"`
}

// GetOrganization GET /v5/organizations/{slug}
func (c *APIClient) GetOrganization(ctx context.Context, slug string) (*Organization, error) {
	var org Organization
	if err := c.doGet(ctx, "/v5/organizations/"+url.PathEscape(slug), &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// listPaymentsPage : 1 page de résultats.
type listPaymentsPage struct {
	Data       []Payment `json:"data"`
	Pagination struct {
		PageIndex   int    `json:"pageIndex"`
		PageSize    int    `json:"pageSize"`
		TotalCount  int    `json:"totalCount"`
		ContinuationToken string `json:"continuationToken,omitempty"`
	} `json:"pagination"`
}

// ListPayments GET /v5/organizations/{slug}/payments avec pagination cursor.
// Retourne tous les paiements entre [since, until] (RFC3339), max maxPages pages.
func (c *APIClient) ListPayments(ctx context.Context, slug, since, until string, maxPages int) ([]Payment, error) {
	if maxPages <= 0 {
		maxPages = 10
	}
	out := []Payment{}
	cursor := ""
	for i := 0; i < maxPages; i++ {
		q := url.Values{}
		q.Set("pageSize", "100")
		if since != "" {
			q.Set("from", since)
		}
		if until != "" {
			q.Set("to", until)
		}
		if cursor != "" {
			q.Set("continuationToken", cursor)
		}
		path := "/v5/organizations/" + url.PathEscape(slug) + "/payments?" + q.Encode()
		var page listPaymentsPage
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Data...)
		if page.Pagination.ContinuationToken == "" || len(page.Data) == 0 {
			break
		}
		cursor = page.Pagination.ContinuationToken
	}
	return out, nil
}

// GetPayment GET /v5/payments/{id}
func (c *APIClient) GetPayment(ctx context.Context, paymentID int64) (*Payment, error) {
	var p Payment
	if err := c.doGet(ctx, "/v5/payments/"+strconv.FormatInt(paymentID, 10), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListForms GET /v5/organizations/{slug}/forms
func (c *APIClient) ListForms(ctx context.Context, slug string) ([]Form, error) {
	var resp struct {
		Data []Form `json:"data"`
	}
	if err := c.doGet(ctx, "/v5/organizations/"+url.PathEscape(slug)+"/forms?pageSize=100", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// doGet exécute GET path, parse JSON, gère 429 backoff.
func (c *APIClient) doGet(ctx context.Context, path string, out any) error {
	url := c.BaseURL + path
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt == 0 {
				time.Sleep(5 * time.Second)
				continue
			}
			return fmt.Errorf("helloasso: rate limit dépassé sur %s (429 après retry)", path)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("helloasso: HTTP %d sur %s : %s", resp.StatusCode, path, truncStr(string(body), 200))
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(body, out)
	}
	return fmt.Errorf("helloasso: échec après 2 tentatives sur %s", path)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
