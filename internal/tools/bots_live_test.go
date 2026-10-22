package tools

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"viktor/internal/client"
)

// TestLiveBotAttribution verifies the agent-side of card #616 with the
// pre-minted bot identity (VIKTOR_BOT_NAME/VIKTOR_BOT_TOKEN from .env):
// the operator shares a probe project to the bot, the bot acts under its
// own token, and the action is attributed to the bot user. Management
// (create/disable/mint) needs a JWT session and is covered by
// TestLiveBots once owner credentials are available.
func TestLiveBotAttribution(t *testing.T) {
	url := os.Getenv("VIKUNJA_URL")
	token := os.Getenv("VIKUNJA_TOKEN")
	botName := os.Getenv("VIKTOR_BOT_NAME")
	botToken := os.Getenv("VIKTOR_BOT_TOKEN")
	if url == "" || token == "" || botName == "" || botToken == "" {
		t.Skip("live test: set VIKUNJA_URL, VIKUNJA_TOKEN, VIKTOR_BOT_NAME, VIKTOR_BOT_TOKEN")
	}

	api := client.New(url, token, 0)
	ctx := context.Background()

	botAPI := client.New(url, botToken, 0)
	var me map[string]any
	if err := botAPI.Get(ctx, "/user", nil, &me); err != nil {
		t.Skipf("bot token not usable (revoked or malformed .env?): %v", err)
	}
	botID := int(me["id"].(float64))
	if me["username"] != botName {
		t.Fatalf("bot token belongs to %v, want %s", me["username"], botName)
	}
	if int(me["bot_owner_id"].(float64)) == 0 {
		t.Fatal("authenticated user is not a bot (bot_owner_id=0)")
	}

	var proj map[string]any
	if err := api.CreateProject(ctx, map[string]any{"title": fmt.Sprintf("viktor-bot-attrib-%d", time.Now().Unix())}, &proj); err != nil {
		t.Skipf("live create unavailable: %v", err)
	}
	projID := int(proj["id"].(float64))
	defer func() { _ = api.DeleteProject(ctx, projID) }()

	if err := api.ShareProject(ctx, projID, map[string]any{"username": botName, "permission": 1}, nil); err != nil {
		t.Fatalf("share read+write to bot: %v", err)
	}

	var task map[string]any
	if err := botAPI.CreateTask(ctx, projID, map[string]any{"title": "attributed action"}, &task); err != nil {
		t.Fatalf("bot cannot act under its own token: %v", err)
	}
	createdByID := int(task["created_by"].(map[string]any)["id"].(float64))
	if createdByID != botID {
		t.Fatalf("attribution failed: created_by=%d, want bot id %d", createdByID, botID)
	}

	// Read-back as the bot to confirm the permission actually granted
	// write scope (permission=1), not just read.
	if err := botAPI.GetTask(ctx, int(task["id"].(float64)), nil); err != nil {
		t.Fatalf("bot read-back: %v", err)
	}
}
