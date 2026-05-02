// CLAUDE:SUMMARY Test gardien M-ASSOKIT-AUDIT-FIX-1 Fix 6 — slog mcp_tool_* avec req_id, user_id, duration_ms.
package actions

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/pkg/horui/auth"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"

	_ "modernc.org/sqlite"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ctxWithPerm injecte un user + RBAC service avec la perm donnée accordée.
func ctxWithPerm(t *testing.T, userID, perm string) (context.Context, *rbac.Service) {
	t.Helper()
	db := openMemDB(t)
	svc := &rbac.Service{Store: &rbac.Store{DB: db}, Cache: &rbac.Cache{}}
	bg := context.Background()
	gID, _ := svc.Store.CreateGrade(bg, "g-"+userID)
	pID, _ := svc.Store.EnsurePermission(bg, perm, "")
	_ = svc.Store.GrantPerm(bg, gID, pID)
	_ = svc.Store.AssignGrade(bg, userID, gID)
	_ = svc.Recompute(bg, userID)

	ctx := middleware.ContextWithUser(
		perms.ContextWithUserID(perms.ContextWithService(bg, svc), userID),
		&auth.User{ID: userID},
	)
	return ctx, svc
}

// TestMCPToolCallLogsActionAndDuration : appel MCP réussi → slog Info "mcp_tool_call"
// avec req_id, user_id, action, duration_ms numérique non vide.
func TestMCPToolCallLogsActionAndDuration(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, _ := ctxWithPerm(t, "user-mcp-log", "mcp.test.log")
	ctx = middleware.WithRequestID(ctx, "req-test-mcp-1")

	deps := app.AppDeps{Logger: logger}
	action := Action{
		ID:           "mcp.test.log",
		RequiredPerm: "mcp.test.log",
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (Result, error) {
			return Result{Status: "ok", Message: "pong"}, nil
		},
	}

	res, err := invokeMCPAction(ctx, deps, action, []byte(`{}`))
	if err != nil {
		t.Fatalf("invokeMCPAction: %v", err)
	}
	if res == nil {
		t.Fatal("résultat nil")
	}

	out := buf.String()
	if !strings.Contains(out, `"msg":"mcp_tool_call"`) {
		t.Errorf("log ne contient pas msg=mcp_tool_call : %s", out)
	}
	if !strings.Contains(out, `"action":"mcp.test.log"`) {
		t.Errorf("log ne contient pas action=mcp.test.log : %s", out)
	}
	if !strings.Contains(out, `"req_id":"req-test-mcp-1"`) {
		t.Errorf("log ne contient pas req_id : %s", out)
	}
	if !strings.Contains(out, `"user_id":"user-mcp-log"`) {
		t.Errorf("log ne contient pas user_id : %s", out)
	}
	if !strings.Contains(out, `"duration_ms":`) {
		t.Errorf("log ne contient pas duration_ms : %s", out)
	}
}

// TestMCPToolCallLogsDeniedPerm : sans perm → slog Warn "mcp_tool_denied_perm".
func TestMCPToolCallLogsDeniedPerm(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// User sans la perm requise (perm "denied.target" pas accordée).
	ctx, _ := ctxWithPerm(t, "user-deny", "other.perm")

	deps := app.AppDeps{Logger: logger}
	action := Action{
		ID:           "denied.action",
		RequiredPerm: "denied.target",
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (Result, error) {
			t.Fatal("Run ne doit jamais être appelé en cas de deny")
			return Result{}, nil
		},
	}

	_, _ = invokeMCPAction(ctx, deps, action, []byte(`{}`))

	out := buf.String()
	if !strings.Contains(out, `"msg":"mcp_tool_denied_perm"`) {
		t.Errorf("log ne contient pas mcp_tool_denied_perm : %s", out)
	}
	if !strings.Contains(out, `"missing_perm":"denied.target"`) {
		t.Errorf("log ne contient pas missing_perm : %s", out)
	}
}

// TestMCPToolCallLogsFailed : action.Run retourne err → slog Error "mcp_tool_call_failed".
func TestMCPToolCallLogsFailed(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, _ := ctxWithPerm(t, "user-fail", "mcp.fail.test")

	deps := app.AppDeps{Logger: logger}
	action := Action{
		ID:           "mcp.fail.test",
		RequiredPerm: "mcp.fail.test",
		Run: func(_ context.Context, _ app.AppDeps, _ json.RawMessage) (Result, error) {
			return Result{}, errors.New("simulated failure")
		},
	}

	_, _ = invokeMCPAction(ctx, deps, action, []byte(`{}`))

	out := buf.String()
	if !strings.Contains(out, `"msg":"mcp_tool_call_failed"`) {
		t.Errorf("log ne contient pas mcp_tool_call_failed : %s", out)
	}
	if !strings.Contains(out, `"err":"simulated failure"`) {
		t.Errorf("log ne contient pas err : %s", out)
	}
}
