package actions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
	"github.com/hazyhaar/assokit/pkg/horui/perms"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MountMCP enregistre chaque action du registry comme tool MCP.
// Chaque appel vérifie la permission RBAC, exécute l'action et insère une ligne dans mcp_invocations.
func MountMCP(mcpServer *server.MCPServer, deps app.AppDeps, reg *Registry) {
	for _, a := range reg.All() {
		action := a

		var tool mcp.Tool
		if action.ParamsSchema != nil {
			schemaJSON, err := json.Marshal(action.ParamsSchema)
			if err == nil {
				tool = mcp.NewToolWithRawSchema(action.ID, action.Description, schemaJSON)
			} else {
				tool = mcp.NewTool(action.ID, mcp.WithDescription(action.Description))
			}
		} else {
			tool = mcp.NewTool(action.ID, mcp.WithDescription(action.Description))
		}

		mcpServer.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			argsJSON, _ := json.Marshal(req.GetArguments())
			return invokeMCPAction(ctx, deps, action, argsJSON)
		})
	}
}

// invokeMCPAction exécute une action depuis le pipeline MCP : check perm,
// run action, insertInvocation, slog structuré (mcp_tool_*). Extrait pour
// testabilité unitaire (TestMCPToolCallLogsActionAndDuration).
func invokeMCPAction(ctx context.Context, deps app.AppDeps, action Action, argsJSON []byte) (*mcp.CallToolResult, error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	reqID := middleware.RequestIDFromContext(ctx)
	userID := ""
	if u := middleware.UserFromContext(ctx); u != nil {
		userID = u.ID
	}

	if !perms.Has(ctx, action.RequiredPerm) {
		logger.Warn("mcp_tool_denied_perm",
			"req_id", reqID, "user_id", userID,
			"action", action.ID, "missing_perm", action.RequiredPerm)
		insertInvocation(ctx, deps.DB, action.ID, nil, "denied", 0, "permission refusée: "+action.RequiredPerm)
		return mcp.NewToolResultError("permission refusée: " + action.RequiredPerm), nil
	}

	paramsHash := hashParams(argsJSON)

	start := time.Now()
	result, err := action.Run(ctx, deps, argsJSON)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		logger.Error("mcp_tool_call_failed",
			"req_id", reqID, "user_id", userID,
			"action", action.ID, "duration_ms", duration, "err", err.Error())
		insertInvocation(ctx, deps.DB, action.ID, &paramsHash, "error", duration, err.Error())
		return mcp.NewToolResultError(err.Error()), nil
	}

	logger.Info("mcp_tool_call",
		"req_id", reqID, "user_id", userID,
		"action", action.ID, "duration_ms", duration, "status", result.Status)
	insertInvocation(ctx, deps.DB, action.ID, &paramsHash, result.Status, duration, "")

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func hashParams(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func insertInvocation(ctx context.Context, db *sql.DB, actionID string, paramsHash *string, status string, durationMs int64, errMsg string) {
	if db == nil {
		return
	}
	hash := ""
	if paramsHash != nil {
		hash = *paramsHash
	}

	// Extraire actor_id depuis le contexte si disponible
	actorID := ""
	if u := middleware.UserFromContext(ctx); u != nil {
		actorID = u.ID
	}

	id := uuid.New().String()
	db.ExecContext(ctx,
		`INSERT INTO mcp_invocations(id, action_id, actor_id, params_hash, result_status, duration_ms, error_msg)
		 VALUES(?,?,?,?,?,?,?)`,
		id, actionID, actorID, hash, status, durationMs, errMsg,
	)
}
