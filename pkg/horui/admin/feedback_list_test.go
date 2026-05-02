package admin

import (
	"strings"
	"testing"
)

func TestIntToStr(t *testing.T) {
	cases := []struct{ n int; want string }{
		{0, "0"}, {1, "1"}, {42, "42"}, {100, "100"}, {-5, "-5"},
	}
	for _, c := range cases {
		if got := intToStr(c.n); got != c.want {
			t.Errorf("intToStr(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestStatusBadgeClass(t *testing.T) {
	for _, s := range []string{"pending", "triaged", "closed", "spam"} {
		cls := statusBadgeClass(s)
		if !strings.HasPrefix(cls, "badge") {
			t.Errorf("statusBadgeClass(%q) = %q, should start with 'badge'", s, cls)
		}
	}
}

func TestCollectSummaryFieldsInternal(t *testing.T) {
	fb := FeedbackRow{ID: "x", PageURL: "/a", MessageSnip: "msg", Status: "pending", CreatedAt: "2026-01-01"}
	if fb.ID != "x" {
		t.Errorf("FeedbackRow ID = %q", fb.ID)
	}
}
