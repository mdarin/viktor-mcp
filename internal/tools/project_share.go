package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func resolveShareUser(users []map[string]any, in ProjectShareIn) (int, string, bool) {
	for _, u := range users {
		id := asInt(u["id"])
		username, _ := u["username"].(string)
		idMatch := in.UserID != 0 && id == in.UserID
		nameMatch := in.Username != "" && username == in.Username
		if idMatch || nameMatch {
			return id, username, true
		}
	}
	return 0, "", false
}

func handleProjectShare(r *Registry) mcp.ToolHandlerFor[ProjectShareIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectShareIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID <= 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		if in.Permission < 0 || in.Permission > 2 {
			return nil, nil, fmt.Errorf("permission must be 0 (read), 1 (write) or 2 (admin), got %d", in.Permission)
		}
		if in.UserID == 0 && in.Username == "" {
			return nil, nil, fmt.Errorf("user_id or username is required")
		}

		if in.UserID == 0 || in.Username == "" {
			var users []map[string]any
			needle := in.Username
			if needle == "" {
				needle = fmt.Sprintf("%d", in.UserID)
			}
			if err := r.api().SearchUsers(ctx, needle, &users); err != nil {
				return nil, nil, fmt.Errorf("resolve user: %w", err)
			}
			id, name, ok := resolveShareUser(users, in)
			if !ok {
				return nil, nil, fmt.Errorf("user not found: user_id=%d username=%q", in.UserID, in.Username)
			}
			in.UserID, in.Username = id, name
		}

		body := map[string]any{
			"user_id":    in.UserID,
			"username":   in.Username,
			"permission": in.Permission,
		}
		if err := r.api().ShareProject(ctx, in.ProjectID, body, nil); err != nil {
			return nil, nil, err
		}

		var shared []map[string]any
		if err := r.api().ProjectUsers(ctx, in.ProjectID, &shared); err != nil {
			return nil, nil, err
		}
		id, name, ok := resolveShareUser(shared, ProjectShareIn{UserID: in.UserID, Username: in.Username})
		if !ok {
			return nil, nil, fmt.Errorf("share not reflected in project users list: %s", in.Username)
		}
		perm := asInt(sharedEntryPermission(shared, id))
		return nil, map[string]any{
			"shared":     true,
			"project_id": in.ProjectID,
			"user_id":    id,
			"username":   name,
			"permission": perm,
		}, nil
	}
}

func sharedEntryPermission(users []map[string]any, userID int) any {
	for _, u := range users {
		if asInt(u["id"]) == userID {
			return u["permission"]
		}
	}
	return nil
}

type ProjectShareTeamIn struct {
	ProjectID  int    `json:"project_id"`
	Action     string `json:"action,omitempty"`
	TeamID     int    `json:"team_id,omitempty"`
	Permission int    `json:"permission,omitempty"`
}

func handleProjectShareTeam(r *Registry) mcp.ToolHandlerFor[ProjectShareTeamIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectShareTeamIn) (*mcp.CallToolResult, any, error) {
		if in.ProjectID <= 0 {
			return nil, nil, fmt.Errorf("project_id is required")
		}
		action := in.Action
		if action == "" {
			action = "share"
		}
		switch action {
		case "list":
			var teams []map[string]any
			if err := r.api().ProjectTeams(ctx, in.ProjectID, &teams); err != nil {
				return nil, nil, err
			}
			rows := make([]map[string]any, 0, len(teams))
			for _, t := range teams {
				rows = append(rows, map[string]any{
					"team_id":    t["id"],
					"name":       t["name"],
					"permission": t["permission"],
				})
			}
			return nil, map[string]any{"project_id": in.ProjectID, "teams": rows, "count": len(rows)}, nil
		case "revoke":
			if in.TeamID == 0 {
				return nil, nil, fmt.Errorf("team_id is required for revoke")
			}
			if err := r.api().UnshareProjectTeam(ctx, in.ProjectID, in.TeamID); err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"project_id": in.ProjectID, "team_id": in.TeamID, "revoked": true}, nil
		case "share":
			if in.TeamID == 0 {
				return nil, nil, fmt.Errorf("team_id is required for share")
			}
			if in.Permission < 0 || in.Permission > 2 {
				return nil, nil, fmt.Errorf("permission must be 0 (read), 1 (write) or 2 (admin), got %d", in.Permission)
			}
			if err := r.api().ShareProjectTeam(ctx, in.ProjectID, map[string]any{"team_id": in.TeamID, "permission": in.Permission}, nil); err != nil {
				return nil, nil, err
			}
			var teams []map[string]any
			if err := r.api().ProjectTeams(ctx, in.ProjectID, &teams); err != nil {
				return nil, nil, err
			}
			var perm any
			found := false
			for _, t := range teams {
				if asInt(t["id"]) == in.TeamID {
					perm = t["permission"]
					found = true
				}
			}
			if !found {
				return nil, nil, fmt.Errorf("share not reflected in project teams list: team %d", in.TeamID)
			}
			return nil, map[string]any{
				"shared":     true,
				"project_id": in.ProjectID,
				"team_id":    in.TeamID,
				"permission": perm,
			}, nil
		default:
			return nil, nil, fmt.Errorf("unknown action %q (allowed: share, list, revoke)", action)
		}
	}
}
