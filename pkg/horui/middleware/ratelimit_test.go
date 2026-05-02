package middleware_test

import (
	"context"
	"testing"

	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

func TestRateLimiter_AllowsUpToMax(t *testing.T) {
	rl := middleware.NewRateLimiter()
	key := "test-key"

	for i := 0; i < middleware.RateLimitMax; i++ {
		if !rl.Allow(key) {
			t.Fatalf("Allow returned false at attempt %d (max=%d)", i+1, middleware.RateLimitMax)
		}
	}
}

func TestRateLimiter_BlocksAfterMax(t *testing.T) {
	rl := middleware.NewRateLimiter()
	key := "block-key"

	for i := 0; i < middleware.RateLimitMax; i++ {
		rl.Allow(key) //nolint:errcheck
	}

	if rl.Allow(key) {
		t.Error("Allow devrait retourner false après avoir dépassé la limite")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := middleware.NewRateLimiter()

	for i := 0; i < middleware.RateLimitMax; i++ {
		rl.Allow("key-a") //nolint:errcheck
	}

	// key-b ne doit pas être affectée par key-a
	if !rl.Allow("key-b") {
		t.Error("key-b devrait être autorisée indépendamment de key-a")
	}
}

func TestPageURIContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = middleware.WithPageURI(ctx, "/some/path?q=test")

	got := middleware.PageURIFromContext(ctx)
	if got != "/some/path?q=test" {
		t.Errorf("PageURIFromContext = %q, want %q", got, "/some/path?q=test")
	}
}

func TestPageURIContext_EmptyWhenAbsent(t *testing.T) {
	got := middleware.PageURIFromContext(context.Background())
	if got != "" {
		t.Errorf("PageURIFromContext vide context = %q, want \"\"", got)
	}
}
