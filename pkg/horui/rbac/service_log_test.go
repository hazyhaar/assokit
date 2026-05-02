// CLAUDE:SUMMARY Test gardien rbac_check Debug log : allowed=false logué quand Can() deny (M-ASSOKIT-AUDIT-FIX-1).
package rbac

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestRBACCheckLogsDebug : Can() deny → log Debug "rbac_check" avec allowed=false.
func TestRBACCheckLogsDebug(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE permissions (id TEXT PRIMARY KEY, name TEXT UNIQUE, description TEXT NOT NULL DEFAULT '');
		CREATE TABLE user_effective_permissions (user_id TEXT, permission_id TEXT, PRIMARY KEY(user_id, permission_id));
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := &Service{
		Store:  &Store{DB: db},
		Cache:  &Cache{},
		Logger: logger,
	}

	allowed, err := svc.Can(context.Background(), "user-X", "perm.deny.test")
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if allowed {
		t.Fatal("attendu deny, got allowed=true")
	}

	output := buf.String()
	if !strings.Contains(output, "rbac_check") {
		t.Errorf("output ne contient pas 'rbac_check' : %s", output)
	}
	if !strings.Contains(output, `"allowed":false`) {
		t.Errorf("output ne contient pas '\"allowed\":false' : %s", output)
	}
	if !strings.Contains(output, `"perm":"perm.deny.test"`) {
		t.Errorf("output ne contient pas le perm name : %s", output)
	}
}

// TestRBACCheck_NoLogIfLevelInfo : niveau Info → pas de log rbac_check (hot path silencieux).
func TestRBACCheck_NoLogIfLevelInfo(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE permissions (id TEXT PRIMARY KEY, name TEXT UNIQUE, description TEXT NOT NULL DEFAULT '');
		CREATE TABLE user_effective_permissions (user_id TEXT, permission_id TEXT, PRIMARY KEY(user_id, permission_id));
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc := &Service{
		Store:  &Store{DB: db},
		Cache:  &Cache{},
		Logger: logger,
	}

	_, _ = svc.Can(context.Background(), "user-Y", "any.perm")

	if strings.Contains(buf.String(), "rbac_check") {
		t.Errorf("rbac_check loggé en niveau Info (attendu Debug only) : %s", buf.String())
	}
}
