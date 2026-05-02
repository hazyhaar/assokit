package components_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/components"
)

func TestFeedbackWidget_ContainsFormTrigger(t *testing.T) {
	var buf bytes.Buffer
	if err := components.FeedbackWidget("/une-page", "Titre page").Render(context.Background(), &buf); err != nil {
		t.Fatalf("FeedbackWidget render: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, `hx-get=`) {
		t.Error("FeedbackWidget devrait contenir hx-get")
	}
	if !strings.Contains(body, "/feedback/form") {
		t.Error("FeedbackWidget devrait référencer /feedback/form")
	}
	if !strings.Contains(body, "feedback-modal") {
		t.Error("FeedbackWidget devrait contenir l'id feedback-modal")
	}
	if !strings.Contains(body, "Feedback") {
		t.Error("FeedbackWidget devrait afficher le label Feedback")
	}
}

func TestFeedbackForm_ContainsRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	if err := components.FeedbackForm("/test-url", "Test Title", "csrf-tok-123").Render(context.Background(), &buf); err != nil {
		t.Fatalf("FeedbackForm render: %v", err)
	}
	body := buf.String()

	checks := []struct {
		label   string
		content string
	}{
		{"action=/feedback", `action="/feedback"`},
		{"name=message", `name="message"`},
		{"hidden page_url", `name="page_url"`},
		{"hidden page_title", `name="page_title"`},
		{"csrf hidden", `name="_csrf"`},
		{"honeypot", `name="website"`},
		{"honeypot hidden", `display:none`},
		{"csrf value", "csrf-tok-123"},
		{"page_url value", "/test-url"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.content) {
			t.Errorf("FeedbackForm manque %s (%q)", c.label, c.content)
		}
	}
}

func TestFeedbackSuccess_ContainsConfirmation(t *testing.T) {
	var buf bytes.Buffer
	if err := components.FeedbackSuccess().Render(context.Background(), &buf); err != nil {
		t.Fatalf("FeedbackSuccess render: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, "Merci") {
		t.Error("FeedbackSuccess devrait contenir 'Merci'")
	}
	if !strings.Contains(body, `role="status"`) {
		t.Error("FeedbackSuccess devrait avoir role=status pour l'accessibilité")
	}
}
