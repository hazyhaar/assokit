// CLAUDE:SUMMARY Tests gardiens M-ASSOKIT-AUDIT-FIX-3 Axe 2b : 3 stubs MCP implémentés (forum.user.warn, forum.user.timeout, profile.avatar_upload) doivent INSERT en DB.
package seeds_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/actions"
	"github.com/hazyhaar/assokit/pkg/actions/seeds"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"

	_ "modernc.org/sqlite"
)

func setupSeedsDB(t *testing.T) (*sql.DB, app.AppDeps) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		INSERT INTO users(id, email, password_hash, display_name)
		VALUES('admin-1', 'admin@test', 'h', 'Admin'),
		      ('victim-1', 'victim@test', 'h', 'Victim')
	`); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	return db, app.AppDeps{DB: db}
}

func findAction(t *testing.T, id string) actions.Action {
	t.Helper()
	reg := actions.NewRegistry()
	seeds.InitAll(reg)
	for _, a := range reg.All() {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("action %q non trouvée dans le registry", id)
	return actions.Action{}
}

// TestForumUserWarn_InsertsRowInDB : invoke forum.user.warn → row dans forum_warnings.
func TestForumUserWarn_InsertsRowInDB(t *testing.T) {
	db, deps := setupSeedsDB(t)
	action := findAction(t, "forum.user.warn")

	ctx := middleware.ContextWithUser(context.Background(), &auth.User{ID: "admin-1"})
	res, err := action.Run(ctx, deps, json.RawMessage(`{"user_id":"victim-1","reason":"spam"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "ok" {
		t.Errorf("status = %q, want ok ; msg=%s", res.Status, res.Message)
	}

	var count int
	var reason, issuedBy string
	if err := db.QueryRow(`SELECT COUNT(*), reason, issued_by FROM forum_warnings WHERE user_id='victim-1'`).Scan(&count, &reason, &issuedBy); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 1 {
		t.Errorf("count forum_warnings = %d, want 1", count)
	}
	if reason != "spam" {
		t.Errorf("reason = %q, want 'spam'", reason)
	}
	if issuedBy != "admin-1" {
		t.Errorf("issued_by = %q, want 'admin-1'", issuedBy)
	}
}

// TestForumUserTimeout_UpsertsRow : invoke forum.user.timeout → row dans forum_timeouts (UPSERT).
func TestForumUserTimeout_UpsertsRow(t *testing.T) {
	db, deps := setupSeedsDB(t)
	action := findAction(t, "forum.user.timeout")

	ctx := middleware.ContextWithUser(context.Background(), &auth.User{ID: "admin-1"})
	res, err := action.Run(ctx, deps, json.RawMessage(`{"user_id":"victim-1","hours":24,"reason":"flood"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "ok" {
		t.Errorf("status = %q, want ok", res.Status)
	}

	var reason, expiresAt string
	if err := db.QueryRow(`SELECT reason, expires_at FROM forum_timeouts WHERE user_id='victim-1'`).Scan(&reason, &expiresAt); err != nil {
		t.Fatalf("select timeout: %v", err)
	}
	if reason != "flood" {
		t.Errorf("reason = %q, want 'flood'", reason)
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("expires_at parse: %v", err)
	}
	delta := time.Until(exp)
	if delta < 23*time.Hour || delta > 25*time.Hour {
		t.Errorf("expires_at delta = %v, attendu ~24h", delta)
	}

	// 2e invocation : UPSERT → 1 seule row.
	_, err = action.Run(ctx, deps, json.RawMessage(`{"user_id":"victim-1","hours":48,"reason":"recidiv"}`))
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	var count int
	var newReason string
	_ = db.QueryRow(`SELECT COUNT(*), reason FROM forum_timeouts WHERE user_id='victim-1'`).Scan(&count, &newReason)
	if count != 1 {
		t.Errorf("UPSERT raté : count = %d, want 1", count)
	}
	if newReason != "recidiv" {
		t.Errorf("UPSERT n'a pas mis à jour reason : %q", newReason)
	}
}

// TestProfileAvatarUpload_UpsertsAvatar : invoke profile.avatar_upload → row dans user_avatars.
func TestProfileAvatarUpload_UpsertsAvatar(t *testing.T) {
	db, deps := setupSeedsDB(t)
	action := findAction(t, "profile.avatar_upload")

	ctx := middleware.ContextWithUser(context.Background(), &auth.User{ID: "victim-1"})
	res, err := action.Run(ctx, deps, json.RawMessage(`{"avatar_url":"https://cdn.example/v1.png"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "ok" {
		t.Errorf("status = %q, want ok", res.Status)
	}

	var url string
	if err := db.QueryRow(`SELECT avatar_url FROM user_avatars WHERE user_id='victim-1'`).Scan(&url); err != nil {
		t.Fatalf("select avatar: %v", err)
	}
	if url != "https://cdn.example/v1.png" {
		t.Errorf("avatar_url = %q", url)
	}

	// UPSERT : 2e upload écrase la valeur, 1 seule row.
	_, _ = action.Run(ctx, deps, json.RawMessage(`{"avatar_url":"https://cdn.example/v2.png"}`))
	var count int
	var newURL string
	_ = db.QueryRow(`SELECT COUNT(*), avatar_url FROM user_avatars WHERE user_id='victim-1'`).Scan(&count, &newURL)
	if count != 1 {
		t.Errorf("UPSERT raté: count=%d", count)
	}
	if newURL != "https://cdn.example/v2.png" {
		t.Errorf("UPSERT n'a pas écrasé avatar_url : %q", newURL)
	}
}

// TestProfileAvatarUpload_RequiresAuthenticatedUser : sans user en ctx → erreur explicite.
func TestProfileAvatarUpload_RequiresAuthenticatedUser(t *testing.T) {
	_, deps := setupSeedsDB(t)
	action := findAction(t, "profile.avatar_upload")

	res, _ := action.Run(context.Background(), deps, json.RawMessage(`{"avatar_url":"https://x"}`))
	if res.Status != "error" {
		t.Errorf("status = %q, want 'error' (no user)", res.Status)
	}
	if !strings.Contains(res.Message, "non authentifié") {
		t.Errorf("message = %q, attendu mention 'non authentifié'", res.Message)
	}
}
