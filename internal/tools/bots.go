package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type BotListIn struct{}

type BotCreateIn struct {
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

type BotDisableIn struct {
	BotID int `json:"bot_id"`
}

type BotDeleteIn struct {
	BotID int `json:"bot_id"`
}

type BotTokenCreateIn struct {
	BotID         int                 `json:"bot_id"`
	Title         string              `json:"title"`
	ExpiresInDays int                 `json:"expires_in_days,omitempty"`
	Permissions   map[string][]string `json:"permissions,omitempty"`
}

// jwtRequired explains the 401 the upstream returns for user/bot
// management under an API token (card #892): these endpoints accept only
// the owner's JWT, never a tk_ API token.
func jwtRequired(r *Registry) error {
	if r.api().AuthKind() == "api-token" {
		return fmt.Errorf("bot_* requires the owner's JWT: the configured Vikunja API token (tk_...) is rejected by upstream user-management endpoints with 401; configure a JWT (from UI login) for this instance to manage bots")
	}
	return nil
}

// defaultBotTokenDays keeps minted bot tokens long-lived but finite:
// upstream requires a non-null expiry, so "no expiry" is unreachable
// (spike card #54); we default to a year and surface the rotation hint.
const defaultBotTokenDays = 365

const maxBotTokenDays = 3650

func handleBotList(r *Registry) mcp.ToolHandlerFor[BotListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ BotListIn) (*mcp.CallToolResult, any, error) {
		if err := jwtRequired(r); err != nil {
			return nil, nil, err
		}
		var bots []map[string]any
		if err := r.api().ListBots(ctx, &bots); err != nil {
			return nil, nil, err
		}
		if bots == nil {
			bots = []map[string]any{}
		}
		return nil, map[string]any{"bots": bots, "count": len(bots)}, nil
	}
}

func handleBotCreate(r *Registry) mcp.ToolHandlerFor[BotCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BotCreateIn) (*mcp.CallToolResult, any, error) {
		if in.Username == "" {
			return nil, nil, fmt.Errorf("username is required")
		}
		if err := jwtRequired(r); err != nil {
			return nil, nil, err
		}
		// Upstream requirement: bot usernames must start with "bot-".
		if !strings.HasPrefix(in.Username, "bot-") {
			return nil, nil, fmt.Errorf("bot username must start with 'bot-' prefix, got %q", in.Username)
		}
		body := map[string]any{"username": in.Username}
		if in.Name != "" {
			body["name"] = in.Name
		}
		if in.Email != "" {
			body["email"] = in.Email
		}
		var created map[string]any
		if err := r.api().CreateBot(ctx, body, &created); err != nil {
			return nil, nil, err
		}
		created["next_step"] = "mint a token with bot_token_create and share projects via project_share"
		return nil, created, nil
	}
}

func handleBotDisable(r *Registry) mcp.ToolHandlerFor[BotDisableIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BotDisableIn) (*mcp.CallToolResult, any, error) {
		if in.BotID == 0 {
			return nil, nil, fmt.Errorf("bot_id is required")
		}
		if err := jwtRequired(r); err != nil {
			return nil, nil, err
		}
		var bot map[string]any
		if err := r.api().UpdateBot(ctx, in.BotID, map[string]any{"status": 2}, &bot); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"bot_id":  in.BotID,
			"status":  bot["status"],
			"enabled": false,
		}, nil
	}
}

func handleBotDelete(r *Registry) mcp.ToolHandlerFor[BotDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BotDeleteIn) (*mcp.CallToolResult, any, error) {
		if in.BotID == 0 {
			return nil, nil, fmt.Errorf("bot_id is required")
		}
		if err := jwtRequired(r); err != nil {
			return nil, nil, err
		}
		if err := r.api().DeleteBot(ctx, in.BotID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "bot_id": in.BotID}, nil
	}
}

func handleBotTokenCreate(r *Registry) mcp.ToolHandlerFor[BotTokenCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in BotTokenCreateIn) (*mcp.CallToolResult, any, error) {
		if in.BotID == 0 {
			return nil, nil, fmt.Errorf("bot_id is required")
		}
		if in.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		if err := jwtRequired(r); err != nil {
			return nil, nil, err
		}
		days := in.ExpiresInDays
		if days <= 0 {
			days = defaultBotTokenDays
		}
		if days > maxBotTokenDays {
			return nil, nil, fmt.Errorf("expires_in_days=%d exceeds max %d; mint long-lived tokens with a far date and rotate, do not use unbounded lifetimes", days, maxBotTokenDays)
		}
		body := map[string]any{
			"title":      in.Title,
			"owner_id":   in.BotID,
			"expires_at": time.Now().UTC().AddDate(0, 0, days).Format(time.RFC3339),
		}
		if len(in.Permissions) > 0 {
			body["permissions"] = in.Permissions
		}
		var created map[string]any
		if err := r.api().CreateToken(ctx, body, &created); err != nil {
			return nil, nil, err
		}
		// The cleartext token is only readable in this response; the
		// registry logger logs tool name/status only, never results.
		created["owner_id"] = in.BotID
		created["expires_in_days"] = days
		created["rotation_hint"] = fmt.Sprintf("token expires in %d days — schedule rotation", days)
		return nil, created, nil
	}
}

func registerBots(r *Registry) {
	add[BotListIn](r, "bot_list", "List bot users owned by the authenticated operator", handleBotList(r))
	add[BotCreateIn](r, "bot_create", "Create a bot user (username must start with 'bot-')", handleBotCreate(r))
	add[BotDisableIn](r, "bot_disable", "Disable a bot user (status=2); revokes its tokens' access immediately", handleBotDisable(r))
	add[BotDeleteIn](r, "bot_delete", "Delete a bot user permanently", handleBotDelete(r))
	add[BotTokenCreateIn](r, "bot_token_create", "Mint a long-lived API token for a bot user (returned once)", handleBotTokenCreate(r))
}
