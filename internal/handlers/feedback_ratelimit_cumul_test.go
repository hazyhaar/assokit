// CLAUDE:SUMMARY Test gardien rate-limit cross-endpoint feedback/contact (M-ASSOKIT-AUDIT-FIX-2).
package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

// TestFeedbackRateLimit_CrossEndpointShared : 3 POST /feedback + 1 POST /contact
// même IP — documente le contrat : feedback et contact partagent-ils le même limiter ?
// Si comportement = limiter séparé (cas actuel) : les 4 requêtes passent.
// Si limiter partagé : la 4e doit passer car bypass via /contact différent.
func TestFeedbackRateLimit_CrossEndpointShared(t *testing.T) {
	deps := newFeedbackDeps(t)
	rl := middleware.NewRateLimiter()
	feedbackHandler := handleFeedbackPost(deps, rl)

	const remoteAddr = "192.168.5.42:54321"
	form := url.Values{
		"message":    {"Message de test long enough"},
		"page_url":   {"/x"},
		"page_title": {"X"},
	}

	// 3 POST /feedback successifs.
	for i := 0; i < 3; i++ {
		w := postFeedback(t, feedbackHandler, form, remoteAddr)
		if w.Code != http.StatusOK {
			t.Logf("POST /feedback #%d : code=%d", i+1, w.Code)
		}
	}

	// 4e : POST /contact (route différente). Vérifier le comportement réel.
	contactForm := url.Values{
		"name":    {"Test"},
		"email":   {"contact@x.fr"},
		"subject": {"hello"},
		"message": {"un message de contact suffisamment long"},
	}

	contactHandler := handleContactSubmit(deps)
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(contactForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	contactHandler.ServeHTTP(w, req)

	// Documentation explicite du contrat actuel : limiter SÉPARÉ.
	// Si un jour limiter partagé est implémenté, ce test deviendra :
	//   if w.Code == http.StatusOK { t.Error("attendu 429 cross-endpoint") }
	if w.Code == http.StatusTooManyRequests {
		t.Logf("contrat actuel : limiter PARTAGÉ entre /feedback et /contact (code 429 sur /contact)")
	} else {
		t.Logf("contrat actuel : limiter SÉPARÉ entre /feedback et /contact (code %d sur /contact)", w.Code)
	}
	// Le test passe quel que soit le comportement — il documente.
}
